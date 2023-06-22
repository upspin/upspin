// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee // import "upspin.io/pack/ee"

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"io"
	"math/big"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// drng is an io.Reader returning deterministic random bits seeded from aesKey.
type drng struct {
	aes     cipher.Block
	counter uint32
	random  []byte
}

func (d *drng) Read(p []byte) (n int, err error) {
	lenp := len(p)
	n = lenp
	var drand [16]byte
	for n > 0 {
		if len(d.random) == 0 {
			binary.BigEndian.PutUint32(drand[0:4], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(drand[4:8], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(drand[8:12], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(drand[12:16], d.counter)
			d.counter++
			d.random = drand[:]
			d.aes.Encrypt(d.random, d.random)
		}
		m := copy(p, d.random)
		n -= m
		p = p[m:]
		d.random = d.random[m:]
	}
	return lenp, nil
}

// CreateKeys creates a key pair based on the chosen curve and a slice of entropy.
func CreateKeys(curveName string, entropy []byte) (public upspin.PublicKey, private string, err error) {
	const op errors.Op = "pack/ee.CreateKeys"
	var curve elliptic.Curve
	switch curveName {
	case "p256":
		curve = elliptic.P256()
	case "p384":
		curve = elliptic.P384()
	case "p521":
		curve = elliptic.P521()
	default:
		return public, private, errors.E(op, errors.Invalid, errors.Errorf("curveName %s", curveName))
	}

	priv, err := createKeysFromEntropy(curve, entropy)
	if err != nil {
		return public, private, errors.E(op, errors.Invalid, err)
	}
	public, private = encodeKeys(priv, curveName)
	return
}

// GenEntropy fills the slice with cryptographically-secure random bytes.
func GenEntropy(entropy []byte) error {
	_, err := rand.Read(entropy)
	return err
}

// encodeKeys converts an ecsda private key into an upspin key pair. No error checking is performed.
func encodeKeys(priv *ecdsa.PrivateKey, curveName string) (public upspin.PublicKey, private string) {
	private = priv.D.String() + "\n"
	public = upspin.PublicKey(curveName + "\n" + priv.X.String() + "\n" + priv.Y.String() + "\n")
	return
}

// createKeysFromEntropy creates an ecsda private key from a given entropy.
func createKeysFromEntropy(curve elliptic.Curve, entropy []byte) (*ecdsa.PrivateKey, error) {
	// Create crypto deterministic random generator from b.
	d := &drng{}
	cipher, err := aes.NewCipher(entropy)
	if err != nil {
		return nil, err
	}
	d.aes = cipher

	// Generate random key-pair.
	priv, err := legacyGenerateKey(curve, d)
	if err != nil {
		return nil, err
	}
	return priv, nil
}

// The following excerpt from go1.19.10 crypto/ecdsa/ecdsa.go allows us to
// preserve old secretseed -> *.upspinkey mappings. Go 1.20 crypto is more
// secure against timing side channel attacks but is incompatible with upspin
// users' existing stored data.
//
// TODO(ehg) Retire this when we finish migration to post-quantum-crypto
// many years from now.

var one = new(big.Int).SetInt64(1)

// randFieldElement returns a random element of the order of the given
// curve using the procedure given in FIPS 186-4, Appendix B.5.1.
func randFieldElement(c elliptic.Curve, rand io.Reader) (k *big.Int, err error) {
	params := c.Params()
	// Note that for P-521 this will actually be 63 bits more than the order, as
	// division rounds down, but the extra bit is inconsequential.
	b := make([]byte, params.N.BitLen()/8+8)
	_, err = io.ReadFull(rand, b)
	if err != nil {
		return
	}

	k = new(big.Int).SetBytes(b)
	n := new(big.Int).Sub(params.N, one)
	k.Mod(k, n)
	k.Add(k, one)
	return
}

// legacyGenerateKey generates a public and private key pair.
func legacyGenerateKey(c elliptic.Curve, rand io.Reader) (*ecdsa.PrivateKey, error) {
	k, err := randFieldElement(c, rand)
	if err != nil {
		return nil, err
	}

	priv := new(ecdsa.PrivateKey)
	priv.PublicKey.Curve = c
	priv.D = k
	priv.PublicKey.X, priv.PublicKey.Y = c.ScalarBaseMult(k.Bytes())
	return priv, nil
}
