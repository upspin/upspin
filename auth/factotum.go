package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"math/big"

	"upspin.googlesource.com/upspin.git/key/keyloader"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

var sig0 upspin.Signature // for returning nil

var _ upspin.Factotum = Factotum{}

type Factotum struct {
	strKeyPair    upspin.KeyPair   // string form of key pair
	ecdsaKeyPair  ecdsa.PrivateKey // ecdsa form of key pair
	packingString string
}

func NewFactotum(ctx *upspin.Context) (f *Factotum) {
	ePublicKey, packingString, err := keyloader.ParsePublicKey(ctx.KeyPair.Public)
	if err != nil {
		return nil
	}
	kp := ctx.KeyPair
	if n := len(kp.Private) - 1; kp.Private[n] == '\n' {
		kp.Private = kp.Private[:n]
	}
	var d big.Int
	err = d.UnmarshalText([]byte(kp.Private))
	if err != nil {
		return nil
	}
	return &Factotum{
		strKeyPair:    kp,
		ecdsaKeyPair:  ecdsa.PrivateKey{*ePublicKey, &d},
		packingString: packingString,
	}
}

func (f Factotum) PackingString() string {
	return f.packingString
}

func (f Factotum) FileSign(p upspin.Packing, pathname upspin.PathName, time upspin.Time, dkey, sum []byte) (upspin.Signature, error) {
	log.Printf("factotum.fileSign %s %s %d %x\n", pack.Lookup(p).String(), pathname, time, sum)
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, VerHash(p, pathname, time, dkey, sum))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{r, s}, nil
}

func (f Factotum) ScalarMult(curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int) {
	log.Printf("factotum.scalarMult %d %d\n", x, y)
	return curve.ScalarMult(x, y, f.ecdsaKeyPair.D.Bytes())
}

func (f Factotum) UserSign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change soon to TokenBinding anyway
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{r, s}, nil
}

func VerHash(ciphersuite upspin.Packing, pathname upspin.PathName, time upspin.Time, dkey, cipherSum []byte) []byte {
	b := sha256.Sum256([]byte(fmt.Sprintf("%02x:%s:%d:%x:%x", ciphersuite, pathname, time, dkey, cipherSum)))
	return b[:]
}
