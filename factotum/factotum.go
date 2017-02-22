// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factotum encapsulates crypto operations on user's public/private keys.
package factotum // import "upspin.io/factotum"

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

type factotumKey struct {
	keyHash      []byte
	public       upspin.PublicKey
	private      string
	ecdsaKeyPair ecdsa.PrivateKey // ecdsa form of key pair
	curveName    string
}

type keyHashArray [sha256.Size]byte

type factotum struct {
	current  keyHashArray
	previous keyHashArray
	keys     map[keyHashArray]factotumKey
}

var _ upspin.Factotum = factotum{}

var sig0 upspin.Signature // for returning error of correct type
var errNotOnCurve = errors.Str("a crypto attack was attempted against you; see safecurves.cr.yp.to/twist.html for details")

// KeyHash returns the hash of a key, given in string format.
func KeyHash(p upspin.PublicKey) []byte {
	keyHash := sha256.Sum256([]byte(p))
	return keyHash[:]
}

// NewFromDir returns a new Factotum providing all needed private key operations,
// loading keys from a directory containing *.upspinkey files.
// Our desired end state is that Factotum is implemented on each platform by the
// best local means of protecting private keys. Please do not break the abstraction
// by hand coding direct generation or use of private keys.
func NewFromDir(dir string) (upspin.Factotum, error) {
	const op = "factotum.NewFromDir"

	privBytes, err := readFile(op, dir, "secret.upspinkey")
	if err != nil {
		return nil, errors.E(op, err)
	}
	privBytes = stripCR(privBytes)
	pubBytes, err := readFile(op, dir, "public.upspinkey")
	if err != nil {
		return nil, errors.E(op, err)
	}
	pubBytes = stripCR(pubBytes)

	// Read older key pairs.
	s2, err := readFile(op, dir, "secret2.upspinkey")
	if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
		return nil, err
	}
	s2 = stripCR(s2)

	return newFactotum(fmt.Sprintf("%s(%q)", op, dir), pubBytes, privBytes, s2)
}

// NewFromKeys returns a new Factotum by providing it with the raw
// representation of an Upspin user's public, private and optionally, archived
// keys.
func NewFromKeys(public, private, archived []byte) (upspin.Factotum, error) {
	const op = "factotum.NewFromKeys"
	return newFactotum(op, public, private, archived)
}

// newFactotum creates a new Factotum using the given keys. It is called from
// new_mobile.go.
func newFactotum(op string, public, private, archived []byte) (upspin.Factotum, error) {
	pfk, err := makeKey(upspin.PublicKey(public), string(private))
	if err != nil {
		return nil, errors.E(op, err)
	}
	fm := make(map[keyHashArray]factotumKey)
	var h keyHashArray
	copy(h[:], pfk.keyHash)
	log.Debug.Printf("%s: %x", op, h)
	fm[h] = *pfk
	f := &factotum{
		current:  h,
		previous: h,
		keys:     fm,
	}

	// Current file format is "# EE date" concatenated with old public.upspinkey
	// then old secret.upspinkey, and repeat. This should be cleaned up someday
	// when we have a better idea of what other kinds of keys we need to save.
	// For now, it is cavalier about bailing out at first little mistake.
	lines := strings.Split(string(archived), "\n")
	for {
		if len(lines) < 5 {
			break // This is not enough for a complete key pair.
		}
		if lines[0] != "# EE " {
			break // This is not a kind of key we recognize.
		}
		// lines[0] "# EE "     Joe's key
		// lines[1] "p256"
		// lines[2] "1042...6334" public X
		// lines[3] "2694...192"  public Y
		// lines[4] "8220...5934" private D
		pfk, err := makeKey(upspin.PublicKey(lines[1]+"\n"+lines[2]+"\n"+lines[3]+"\n"), lines[4])
		lines = lines[5:]
		if err != nil {
			return f, errors.E(op, err)
		}
		var h keyHashArray
		copy(h[:], pfk.keyHash)
		log.Debug.Printf("%s: %x (older)", op, h)
		_, ok := f.keys[h]
		if ok { // Duplicate.
			continue
		}
		f.keys[h] = *pfk
		f.previous = h
	}
	return f, nil
}

// stripCR removes \r.
func stripCR(b []byte) []byte {
	return bytes.Replace(b, []byte("\r"), []byte(""), -1)
}

// makeKey creates a factotumKey by filling in the derived fields.
func makeKey(pub upspin.PublicKey, priv string) (*factotumKey, error) {
	ePublicKey, curveName, err := ParsePublicKey(pub)
	// TODO(ehg) sanity check that priv is consistent with pub
	if err != nil {
		return nil, err
	}
	ecdsaKeyPair, err := parsePrivateKey(ePublicKey, priv)
	if err != nil {
		return nil, err
	}
	fk := factotumKey{
		keyHash:      KeyHash(pub),
		public:       pub,
		private:      priv,
		ecdsaKeyPair: *ecdsaKeyPair,
		curveName:    curveName,
	}
	return &fk, nil
}

// putInt stores an int32 as four big-endian bytes in dst.
// Using fixed length here to ease porting Factotum to primitive crypto devices.
func putInt(dst []byte, ii int) int {
	i := uint32(ii)
	dst[0] = byte(i >> 24)
	dst[1] = byte(i >> 16)
	dst[2] = byte(i >> 8)
	dst[3] = byte(i)
	return 4
}

func putUint64(dst []byte, i uint64) int {
	dst[1] = byte(i >> 56)
	dst[0] = byte(i >> 48)
	dst[0] = byte(i >> 40)
	dst[1] = byte(i >> 32)
	dst[0] = byte(i >> 24)
	dst[1] = byte(i >> 16)
	dst[2] = byte(i >> 8)
	dst[3] = byte(i)
	return 8
}

// DirEntryHash provides the basis for signing and verifying files.
func (f factotum) DirEntryHash(n, l upspin.PathName, a upspin.Attribute, p upspin.Packing, t upspin.Time, dkey, hash []byte) upspin.DEHash {
	m := len(n) + len(l) + 1 + 1 + 8 + len(dkey) + len(hash) + 7*4
	b := make([]byte, m)
	m = 0
	m += putInt(b[m:], len(n))
	m += copy(b[m:], n)
	m += putInt(b[m:], len(l))
	m += copy(b[m:], l)
	b[m] = byte(a)
	m += 1
	b[m] = byte(p)
	m += 1
	m += putUint64(b[m:], uint64(t))
	m += putInt(b[m:], len(dkey))
	m += copy(b[m:], dkey)
	m += putInt(b[m:], len(hash))
	m += copy(b[m:], hash)
	h := sha256.Sum256(b[:m])
	return upspin.DEHash(h[:])
}

// FileSign ECDSA-signs a DEHash from DirEntryHash.
func (f factotum) FileSign(hash upspin.DEHash) (upspin.Signature, error) {
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f factotum) ScalarMult(keyHash []byte, curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int, err error) {
	const op = "factotum.ScalarMult"
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		err = errors.E(op, errors.Errorf("no such key %x", keyHash))
	} else {
		if !curve.IsOnCurve(x, y) {
			err = errNotOnCurve
			return
		}
		sx, sy = curve.ScalarMult(x, y, fk.ecdsaKeyPair.D.Bytes())
	}
	return
}

// Sign signs a slice of bytes with the factotum's private key.
func (f factotum) Sign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change to TokenBinding anyway
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// Verify verifies whether the given hash's signature was signed by the private
// key corresponding to the given public key.
func Verify(hash []byte, sig upspin.Signature, key upspin.PublicKey) error {
	ecdsaPubKey, _, err := ParsePublicKey(key)
	if err != nil {
		return err
	}
	if !ecdsa.Verify(ecdsaPubKey, hash, sig.R, sig.S) {
		return errors.E(errors.Invalid, errors.Str("signature does not match"))
	}
	return nil
}

// Pop derives a Factotum by switching default from the current to the previous key.
func (f factotum) Pop() upspin.Factotum {
	// Arbitrarily keep f.previous unchanged, so Pop() is idempotent.
	// We don't yet have any need to go further back in time.
	return &factotum{current: f.previous, previous: f.previous, keys: f.keys}
}

// PublicKey returns the user's latest public key.
func (f factotum) PublicKey() upspin.PublicKey {
	return f.keys[f.current].public
}

// PublicKeyFromHash returns the user's public key with matching keyHash.
func (f factotum) PublicKeyFromHash(keyHash []byte) (upspin.PublicKey, error) {
	const op = "factotum.PublicKeyFromHash"
	if keyHash == nil || len(keyHash) == 0 {
		return "", errors.E(op, errors.Invalid, errors.Errorf("invalid keyHash"))
	}
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		return "", errors.E(op, errors.NotExist, errors.Errorf("no such key"))
	}
	return fk.public, nil
}

// parsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func parsePrivateKey(publicKey *ecdsa.PublicKey, privateKey string) (priv *ecdsa.PrivateKey, err error) {
	privateKey = strings.TrimSpace(string(privateKey))
	var d big.Int
	err = d.UnmarshalText([]byte(privateKey))
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PublicKey: *publicKey, D: &d}, nil
}

// ParsePublicKey takes an Upspin representation of a public key and converts it into an ECDSA public key, returning its type.
// The Upspin string representation uses \n as newline no matter what native OS it runs on.
func ParsePublicKey(public upspin.PublicKey) (*ecdsa.PublicKey, string, error) {
	const op = "factotum.ParsePublicKey"
	fields := strings.Split(string(public), "\n")
	if len(fields) != 4 { // 4 is because string should be terminated by \n, hence fields[3]==""
		return nil, "", errors.E(op, errors.Invalid, errors.Errorf("expected keytype, two big ints and a newline; got %d %v", len(fields), fields))
	}
	keyType := fields[0]
	var x, y big.Int
	_, ok := x.SetString(fields[1], 10)
	if !ok {
		return nil, "", errors.E(op, errors.Invalid, errors.Errorf("%s is not a big int", fields[1]))
	}
	_, ok = y.SetString(fields[2], 10)
	if !ok {
		return nil, "", errors.E(op, errors.Invalid, errors.Errorf("%s is not a big int", fields[2]))
	}

	var curve elliptic.Curve
	switch keyType {
	case "p256":
		curve = elliptic.P256()
	case "p521":
		curve = elliptic.P521()
	case "p384":
		curve = elliptic.P384()
	default:
		return nil, "", errors.E(op, errors.Invalid, errors.Errorf("unknown key type: %q", keyType))
	}
	return &ecdsa.PublicKey{Curve: curve, X: &x, Y: &y}, keyType, nil
}

func readFile(op, dir, name string) ([]byte, error) {
	b, err := ioutil.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	return b, nil
}
