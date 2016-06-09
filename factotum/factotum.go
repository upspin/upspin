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

	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/upspin"
)

var sig0 upspin.Signature // for returning nil

var _ upspin.Factotum = Factotum{}

// Factotum implements upspin.Factotum by reading from ~/.ssh/*.upspinkey.
type Factotum struct {
	strKeyPair    upspin.KeyPair   // string form of key pair
	ecdsaKeyPair  ecdsa.PrivateKey // ecdsa form of key pair
	packingString string
}

// New returns a new Factotum providing all needed private key operations.
func New(kp upspin.KeyPair) (*Factotum, error) {
	ePublicKey, packingString, err := ParsePublicKey(kp.Public)
	if err != nil {
		return nil, err
	}
	ecdsaKeyPair, err := parsePrivateKey(ePublicKey, kp.Private)
	if err != nil {
		return nil, err
	}
	f := &Factotum{
		strKeyPair:    kp,
		ecdsaKeyPair:  *ecdsaKeyPair,
		packingString: packingString,
	}
	return f, nil
}

// PackingString returns the Packing.String() value associated with the key inside f.
func (f Factotum) PackingString() string {
	return f.packingString
}

// FileSign ECDSA-signs p|n|t|dkey|hash, as required for EEp256Pack and similar.
func (f Factotum) FileSign(p upspin.Packing, n upspin.PathName, t upspin.Time, dkey, hash []byte) (upspin.Signature, error) {
	log.Debug.Printf("factotum.fileSign %s %s %d %x\n", pack.Lookup(p).String(), n, t, hash)
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, VerHash(p, n, t, dkey, hash))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f Factotum) ScalarMult(curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int) {
	log.Debug.Printf("factotum.scalarMult %d %d\n", x, y)
	return curve.ScalarMult(x, y, f.ecdsaKeyPair.D.Bytes())
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
	return f.strKeyPair.Public
}

// VerHash provides the basis for signing and verifying files.
func VerHash(ciphersuite upspin.Packing, pathname upspin.PathName, time upspin.Time, dkey, cipherSum []byte) []byte {
	b := sha256.Sum256([]byte(fmt.Sprintf("%02x:%s:%d:%x:%x", ciphersuite, pathname, time, dkey, cipherSum)))
	return b[:]
}

// parsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func parsePrivateKey(publicKey *ecdsa.PublicKey, privateKey upspin.PrivateKey) (priv *ecdsa.PrivateKey, err error) {
	privateKey = upspin.PrivateKey(strings.TrimSpace(string(privateKey)))
	var d big.Int
	err = d.UnmarshalText([]byte(privateKey))
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PublicKey: *publicKey, D: &d}, nil
}

// ParsePublicKey takes an Upspin representation of a public key and converts it into an ECDSA public key, returning its type.
func ParsePublicKey(publicKey upspin.PublicKey) (*ecdsa.PublicKey, string, error) {
	var keyType string
	var x, y big.Int

	n, err := fmt.Fscan(bytes.NewReader([]byte(publicKey)), &keyType, &x, &y)
	if err != nil {
		return nil, "", err
	}
	if n != 3 {
		return nil, "", fmt.Errorf("expected keytype and two big ints, got %d", n)
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
		return nil, "", fmt.Errorf("unknown key type: %q", keyType)
	}
	return &ecdsa.PublicKey{Curve: curve, X: &x, Y: &y}, keyType, nil
}
