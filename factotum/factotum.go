// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factotum encapsulates crypto operations on user's public/private keys.
package factotum

import (
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
	"u-old/log"

	"upspin.io/errors"
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

var sig0 upspin.Signature // for returning nil

// KeyHash returns the hash of a key, given in string format.
func KeyHash(p upspin.PublicKey) []byte {
	keyHash := sha256.Sum256([]byte(p))
	return keyHash[:]
}

// New returns a new Factotum providing all needed private key operations,
// loading keys from dir/*.upspinkey.
// Our desired end state is that Factotum is implemented on each platform by the
// best local means of protecting private keys.  Please do not break the abstraction
// by hand coding direct generation or use of private keys.
func New(dir string) (upspin.Factotum, error) {
	op := "NewFactotum"
	privBytes, err := readFile(op, dir, "secret.upspinkey")
	if err != nil {
		return nil, err
	}
	pubBytes, err := readFile(op, dir, "public.upspinkey")
	if err != nil {
		return nil, err
	}
	pfk, err := makeKey(upspin.PublicKey(pubBytes), string(privBytes))
	if err != nil {
		return nil, err
	}
	fm := make(map[keyHashArray]factotumKey)
	var h keyHashArray
	copy(h[:], pfk.keyHash)
	log.Debug.Printf("factotum %x %q\n", h, pubBytes)
	fm[h] = *pfk
	f := &factotum{
		current:  h,
		previous: h,
		keys:     fm,
	}

	// Read older key pairs.
	// Current file format is "# EE date" concatenated with old public.upspinkey
	// then old secret.upspinkey, and repeat.  This should be cleaned up someday
	// when we have a better idea of what other kinds of keys we need to save.
	// For now, it is cavalier about bailing out at first little mistake.
	s2, err := readFile(op, dir, "secret2.upspinkey")
	if err != nil {
		return f, nil
	}
	lines := strings.Split(string(s2), "\n")
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
		if err != nil {
			break
		}
		var h keyHashArray
		copy(h[:], pfk.keyHash)
		log.Debug.Printf("factotum %x %q\n", h, lines[1]+"\n"+lines[2]+"\n"+lines[3]+"\n")
		_, ok := f.keys[h]
		if ok { // Duplicate.
			continue // TODO Should we warn?
		}
		f.keys[h] = *pfk
		f.previous = h
		lines = lines[5:]
	}
	return f, err
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

// FileSign ECDSA-signs c|n|t|dkey|hash, as required for EEPack.
func (f factotum) FileSign(n upspin.PathName, t upspin.Time, dkey, hash []byte) (upspin.Signature, error) {
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, VerHash(fk.curveName, n, t, dkey, hash))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f factotum) ScalarMult(keyHash []byte, curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int, err error) {
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		err = errors.E("scalarMult", errors.Errorf("no such key %x", keyHash))
	} else {
		sx, sy = curve.ScalarMult(x, y, fk.ecdsaKeyPair.D.Bytes())
	}
	return
}

// UserSign assists in authenticating to Upspin servers.
func (f factotum) UserSign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change to TokenBinding anyway
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
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
	if keyHash == nil || len(keyHash) == 0 {
		return "", errors.Errorf("invalid keyHash")
	}
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		return "", errors.Errorf("no such key")
	}
	return fk.public, nil
}

// VerHash provides the basis for signing and verifying files.
func VerHash(curveName string, pathname upspin.PathName, time upspin.Time, dkey, cipherSum []byte) []byte {
	b := sha256.Sum256([]byte(fmt.Sprintf("%02x:%s:%d:%x:%x", curveName, pathname, time, dkey, cipherSum)))
	return b[:]
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
	fields := strings.Split(string(public), "\n")
	if len(fields) != 4 { // 4 is because string should be terminated by \n, hence fields[3]==""
		return nil, "", errors.E("ParsePublicKey", errors.Invalid, errors.Errorf("expected keytype, two big ints and a newline; got %d %v", len(fields), fields))
	}
	keyType := fields[0]
	var x, y big.Int
	_, ok := x.SetString(fields[1], 10)
	if !ok {
		return nil, "", errors.E("ParsePublicKey", errors.Invalid, errors.Errorf("%s is not a big int", fields[1]))
	}
	_, ok = y.SetString(fields[2], 10)
	if !ok {
		return nil, "", errors.E("ParsePublicKey", errors.Invalid, errors.Errorf("%s is not a big int", fields[2]))
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
		return nil, "", errors.Errorf("unknown key type: %q", keyType)
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
