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
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var sig0 upspin.Signature // for returning nil

// KeyHash returns the hash of a key, given in string format.
func KeyHash(p upspin.PublicKey) []byte {
	keyHash := sha256.Sum256([]byte(p))
	return keyHash[:]
}

var _ upspin.Factotum = Factotum{}

type factotumKey struct {
	keyHash      []byte
	public       upspin.PublicKey
	private      string
	ecdsaKeyPair ecdsa.PrivateKey // ecdsa form of key pair
	curveName    string
}

type keyHashArray [sha256.Size]byte

type Factotum struct {
	current keyHashArray
	keys    map[keyHashArray]factotumKey
}

// New returns a new Factotum providing all needed private key operations,
// loading keys from dir/*.upspinkey.
func New(dir string) (*Factotum, error) {
	op := "NewFactotum"
	priv_bytes, err := ioutil.ReadFile(filepath.Join(dir, "secret.upspinkey"))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	priv := string(priv_bytes) // Let parsePrivateKey do the TrimSpace.
	pub_bytes, err := ioutil.ReadFile(filepath.Join(dir, "public.upspinkey"))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	pub := upspin.PublicKey(pub_bytes)
	f, err := DeprecatedNew(pub, priv)
	if err != nil {
		return nil, errors.E(op, errors.Errorf("unable to load key"), err)
	}

	// Read older key pairs.
	// Current file format is "# EE date" concatenated with old public.upspinkey
	// then old secret.upspinkey, and repeat.  This should be cleaned up someday
	// when we have a better idea of what other kinds of keys we need to save.
	s2, err := ioutil.ReadFile(filepath.Join(dir, "secret2.upspinkey"))
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, errors.E(op, errors.IO, err) // not fatal
	}
	for {
		if string(s2[:5]) != "# EE " {
			break
		}
		// TODO  Surely there is a better syntax for reading lines.
		n := bytes.IndexByte(s2, '\n')
		if n < 0 {
			break
		}
		s2 = s2[n:]
		n = bytes.IndexByte(s2, '\n')      // curve
		j := bytes.IndexByte(s2[n:], '\n') // public.x
		if j < 0 {
			break
		}
		k := bytes.IndexByte(s2[n+j:], '\n') // public.y
		if k < 0 {
			break
		}
		pub := upspin.PublicKey(s2[:n+j+k])
		s2 = s2[n+j+k:]
		n = bytes.IndexByte(s2, '\n')
		if n < 0 {
			break
		}
		priv := string(s2[:n])
		pfk, err := fKey(pub, priv)
		if err != nil {
			break
		}
		var h keyHashArray
		copy(h[:], pfk.keyHash)
		_, ok := f.keys[h]
		if ok { // Duplicate.
			continue // TODO Should we warn?
		}
		f.keys[h] = *pfk
	}

	return f, err
}

// DeprecatedNew returns a new Factotum providing all needed private key operations.
// TODO(ehg)  Replace all uses of DeprecatedNew by New.
func DeprecatedNew(public upspin.PublicKey, private string) (*Factotum, error) {
	pfk, err := fKey(public, private)
	if err != nil {
		return nil, err
	}
	fm := make(map[keyHashArray]factotumKey)
	var h keyHashArray
	copy(h[:], pfk.keyHash)
	fm[h] = *pfk
	f := &Factotum{
		current: h,
		keys:    fm,
	}
	return f, nil
}

func fKey(pub upspin.PublicKey, priv string) (*factotumKey, error) {
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
func (f Factotum) FileSign(n upspin.PathName, t upspin.Time, dkey, hash []byte) (upspin.Signature, error) {
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, VerHash(fk.curveName, n, t, dkey, hash))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f Factotum) ScalarMult(keyHash []byte, curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int, err error) {
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
func (f Factotum) UserSign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change to TokenBinding anyway
	fk := f.keys[f.current]
	r, s, err := ecdsa.Sign(rand.Reader, &fk.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{R: r, S: s}, nil
}

// PublicKey returns the user's public key with matching keyHash.
func (f Factotum) PublicKey(keyHash []byte) upspin.PublicKey {
	if keyHash == nil || len(keyHash) == 0 {
		return f.keys[f.current].public
	}
	var h keyHashArray
	copy(h[:], keyHash)
	fk, ok := f.keys[h]
	if !ok {
		return upspin.PublicKey("")
	}
	return fk.public
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
