// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factotum encapsulates crypto operations on user's public/private keys.
package factotum

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

var sig0 upspin.Signature // for returning nil

// KeyHash returns the hash of a key, given in string format.
func KeyHash(p upspin.PublicKey) []byte {
	keyHash := sha256.Sum256([]byte(p))
	return keyHash[:]
}

var _ upspin.Factotum = Factotum{}

type Factotum struct {
	keyHash      []byte
	public       upspin.PublicKey
	private      string
	ecdsaKeyPair ecdsa.PrivateKey // ecdsa form of key pair
	curveName    string
}

// New returns a new Factotum providing all needed private key operations.
func New(public upspin.PublicKey, private string) (*Factotum, error) {
	ePublicKey, curveName, err := ParsePublicKey(public)
	if err != nil {
		return nil, err
	}
	ecdsaKeyPair, err := parsePrivateKey(ePublicKey, private)
	if err != nil {
		return nil, err
	}
	f := &Factotum{
		keyHash:      KeyHash(public),
		public:       public,
		private:      private,
		ecdsaKeyPair: *ecdsaKeyPair,
		curveName:    curveName,
	}
	return f, nil
}

// PackingString returns the Packing.String() value associated with the key inside f.
func (f Factotum) PackingString() string {
	return f.curveName
}

// FileSign ECDSA-signs c|n|t|dkey|hash, as required for EEPack.
func (f Factotum) FileSign(n upspin.PathName, t upspin.Time, dkey, hash []byte) (upspin.Signature, error) {
	log.Debug.Printf("factotum.fileSign %s %s %d %x\n", f.curveName, n, t, hash)
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, VerHash(f.curveName, n, t, dkey, hash))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f Factotum) ScalarMult(keyHash []byte, curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int, err error) {
	log.Debug.Printf("factotum.scalarMult %x %d %d\n", keyHash, x, y)
	if !bytes.Equal(f.keyHash, keyHash) {
		err = errors.E("scalarMult", errors.Errorf("no such key %x!=%x", f.keyHash, keyHash))
	} else {
		sx, sy = curve.ScalarMult(x, y, f.ecdsaKeyPair.D.Bytes())
	}
	return
}

// UserSign assists in authenticating to Upspin servers.
func (f Factotum) UserSign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change soon to TokenBinding anyway
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// PublicKey returns the user's public key as loaded by the Factotum.
func (f Factotum) PublicKey() upspin.PublicKey {
	return upspin.PublicKey(f.public)
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
