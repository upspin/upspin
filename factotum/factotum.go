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
	"io"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/hkdf"

	"upspin.io/errors"
	"upspin.io/upspin"
)

type factotumKey struct {
	keyHash      []byte
	public       upspin.PublicKey
	private      string
	ecdsaKeyPair ecdsa.PrivateKey
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

// AllUsersKeyHash is the hash of upspin.AllUsersKey.
var AllUsersKeyHash = KeyHash(upspin.AllUsersKey)

// NewFromDir returns a new Factotum providing all needed private key operations,
// loading keys from a directory containing *.upspinkey files.
// Our desired end state is that Factotum is implemented on each platform by the
// best local means of protecting private keys. Please do not break the abstraction
// by hand coding direct generation or use of private keys.
func NewFromDir(dir string) (upspin.Factotum, error) {
	const op errors.Op = "factotum.NewFromDir"

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
	if err != nil && !errors.Is(errors.NotExist, err) {
		return nil, err
	}
	s2 = stripCR(s2)

	return newFactotum(errors.Op(fmt.Sprintf("%s(%q)", op, dir)), pubBytes, privBytes, s2)
}

// NewFromKeys returns a new Factotum by providing it with the raw
// representation of an Upspin user's public, private and optionally, archived
// keys.
func NewFromKeys(public, private, archived []byte) (upspin.Factotum, error) {
	const op errors.Op = "factotum.NewFromKeys"
	return newFactotum(op, public, private, archived)
}

// newFactotum creates a new Factotum using the given keys.
func newFactotum(op errors.Op, public, private, archived []byte) (upspin.Factotum, error) {
	pfk, err := makeKey(upspin.PublicKey(public), string(private))
	if err != nil {
		return nil, errors.E(op, err)
	}
	fm := make(map[keyHashArray]factotumKey)
	var h keyHashArray
	copy(h[:], pfk.keyHash)
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
		if !strings.HasPrefix(lines[0], "# EE") {
			break // This is not a kind of key we recognize.
		}
		// lines[0] "# EE "     Joe's key
		// lines[1] "p256"
		// lines[2] "1042...6334" public X
		// lines[3] "2694...192"  public Y
		// lines[4] "8220...5934" private D
		suffix := strings.Index(lines[4], " ")
		if suffix > 0 {
			lines[4] = lines[4][:suffix]
		}
		pfk, err := makeKey(upspin.PublicKey(lines[1]+"\n"+lines[2]+"\n"+lines[3]+"\n"), lines[4]+"\n")
		if err != nil {
			return f, errors.E(op, err)
		}
		lines = lines[5:]
		var h keyHashArray
		copy(h[:], pfk.keyHash)
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
	ePublicKey, err := ParsePublicKey(pub)
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
	}
	return &fk, nil
}

// putInt stores an int32 as four big-endian bytes in dst.
// Using fixed length here to ease porting Factotum to primitive crypto devices.
// Arguably this should be a call to binary.BigEndian.
func putInt(dst []byte, ii int) int {
	i := uint32(ii)
	dst[0] = byte(i >> 24)
	dst[1] = byte(i >> 16)
	dst[2] = byte(i >> 8)
	dst[3] = byte(i)
	return 4
}

func buggy64(dst []byte, i uint64) int {
	// TODO(ehg) This typo got in by mistake. It should read:
	// dst[0] = byte(i >> 56)
	// dst[1] = byte(i >> 48)
	// dst[2] = byte(i >> 40)
	// dst[3] = byte(i >> 32)
	// dst[4] = byte(i >> 24)
	// dst[5] = byte(i >> 16)
	// dst[6] = byte(i >> 8)
	// dst[7] = byte(i)
	// But the fix would break all existing DirEntry signatures.
	// This function is only used in one place to sign the time
	// field, which we don't currently depend on anyway.

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
	m += buggy64(b[m:], uint64(t))
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
	const op errors.Op = "factotum.ScalarMult"
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
	fk := f.keys[f.current]
	curveLength := (fk.ecdsaKeyPair.Curve.Params().N.BitLen() + 7) / 8
	if len(hash) > curveLength {
		return sig0, errors.E(errors.Invalid, "hash is too long to Sign")
	}
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// Verify verifies whether the given hash's signature was signed by the private
// key corresponding to the given public key.
func Verify(hash []byte, sig upspin.Signature, key upspin.PublicKey) error {
	ecdsaPubKey, err := ParsePublicKey(key)
	if err != nil {
		return err
	}
	if !ecdsa.Verify(ecdsaPubKey, hash, sig.R, sig.S) {
		return errors.E(errors.Invalid, "signature does not match")
	}
	return nil
}

// HKDF cryptographically mixes salt, info, and the Factotum secret and
// writes the result to out, which may be of any length but is typically
// 8 or 16 bytes. The result is unguessable without the secret, and does
// not leak the secret. For more information, see package
// golang.org/x/crypto/hkdf.
func (f factotum) HKDF(salt, info, out []byte) error {
	hash := sha256.New
	secret := []byte(f.keys[f.current].private)
	hkdf := hkdf.New(hash, secret, salt, info)
	_, err := io.ReadFull(hkdf, out)
	return err
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
	const op errors.Op = "factotum.PublicKeyFromHash"
	if len(keyHash) == 0 {
		return "", errors.E(op, errors.Invalid, "invalid keyHash")
	}
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		return "", errors.E(op, errors.NotExist, "no such key")
	}
	return fk.public, nil
}

// clean removes comments and starting and leading space.
func clean(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// parsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func parsePrivateKey(publicKey *ecdsa.PublicKey, privateKey string) (priv *ecdsa.PrivateKey, err error) {
	const op errors.Op = "factotum.PublicKeyFromHash"
	var d big.Int
	err = d.UnmarshalText([]byte(clean(privateKey)))
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}
	x, y := publicKey.Curve.ScalarBaseMult(d.Bytes())
	if x.Cmp(publicKey.X) != 0 || y.Cmp(publicKey.Y) != 0 {
		return nil, errors.E(op, errors.Invalid, "public and private keys do not correspond")
	}
	return &ecdsa.PrivateKey{PublicKey: *publicKey, D: &d}, nil
}

// ParsePublicKey takes an Upspin representation of a public key and converts it into an ECDSA public key.
// The Upspin string representation uses \n as newline no matter what native OS it runs on.
func ParsePublicKey(public upspin.PublicKey) (*ecdsa.PublicKey, error) {
	const op errors.Op = "factotum.ParsePublicKey"
	fields := strings.Split(string(public), "\n")
	if len(fields) != 4 { // 4 is because string should be terminated by \n, hence fields[3]==""
		return nil, errors.E(op, errors.Invalid, errors.Errorf("expected keytype, two big ints and a newline; got %d %v", len(fields), fields))
	}
	keyType := fields[0]
	var x, y big.Int
	_, ok := x.SetString(fields[1], 10)
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%s is not a big int", fields[1]))
	}
	_, ok = y.SetString(fields[2], 10)
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%s is not a big int", fields[2]))
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
		return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown key type: %q", keyType))
	}
	return &ecdsa.PublicKey{Curve: curve, X: &x, Y: &y}, nil
}

func readFile(op errors.Op, dir, name string) ([]byte, error) {
	b, err := ioutil.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	return b, nil
}
