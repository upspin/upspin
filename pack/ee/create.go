package ee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
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

// CreateKeys creates a key pair based on the chosen packing and a slice of entropy.
func CreateKeys(packing upspin.Packing, entropy []byte) (*upspin.KeyPair, error) {
	packer := pack.Lookup(packing)
	if packer == nil {
		return nil, fmt.Errorf("packing %v not registered", packing)
	}
	var keyType string
	var curve elliptic.Curve
	switch packer.(type) {
	case eep256:
		keyType = (packer.(eep256)).packerString
		curve = (packer.(eep256)).curve
	case eep384:
		keyType = (packer.(eep384)).packerString
		curve = (packer.(eep384)).curve
	case eep521:
		keyType = (packer.(eep521)).packerString
		curve = (packer.(eep521)).curve
	default:
		return nil, fmt.Errorf("invalid packing %d", packing)
	}

	priv, err := createKeysFromEntropy(curve, entropy)
	if err != nil {
		return nil, err
	}
	keypair := encodeKeys(priv, keyType)
	return keypair, nil
}

// GenEntropy fills the slice with cryptographically-secure random bytes.
func GenEntropy(entropy []byte) error {
	_, err := rand.Read(entropy)
	if err != nil {
		return err
	}
	return nil
}

// encodeKeys converts an ecsda private key into an upspin key pair. No error checking is performed.
func encodeKeys(priv *ecdsa.PrivateKey, keyType string) *upspin.KeyPair {
	return &upspin.KeyPair{
		Private: upspin.PrivateKey(priv.D.String() + "\n"),
		Public:  upspin.PublicKey(keyType + "\n" + priv.X.String() + "\n" + priv.Y.String() + "\n"),
	}
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
	priv, err := ecdsa.GenerateKey(curve, d)
	if err != nil {
		return nil, fmt.Errorf("key not generated: %s", err)
	}
	return priv, nil
}
