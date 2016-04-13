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

// Factotum implements upspin.Factotum by reading from ~/.ssh/*.upspinkey.
type Factotum struct {
	strKeyPair    upspin.KeyPair   // string form of key pair
	ecdsaKeyPair  ecdsa.PrivateKey // ecdsa form of key pair
	packingString string
}

// NewFactotum returns a new Factotum providing all needed private key operations.
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
		ecdsaKeyPair:  ecdsa.PrivateKey{PublicKey: *ePublicKey, D: &d},
		packingString: packingString,
	}
}

// PackingString returns the Packing.String() value associated with the key inside f.
func (f Factotum) PackingString() string {
	return f.packingString
}

// FileSign ECDSA-signs p|n|t|dkey|hash, as required for EEp256Pack and similar.
func (f Factotum) FileSign(p upspin.Packing, n upspin.PathName, t upspin.Time, dkey, hash []byte) (upspin.Signature, error) {
	log.Printf("factotum.fileSign %s %s %d %x\n", pack.Lookup(p).String(), n, t, hash)
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, VerHash(p, n, t, dkey, hash))
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{r, s}, nil
}

// ScalarMult is the bare private key operator, used in unwrapping packed data.
func (f Factotum) ScalarMult(curve elliptic.Curve, x, y *big.Int) (sx, sy *big.Int) {
	log.Printf("factotum.scalarMult %d %d\n", x, y)
	return curve.ScalarMult(x, y, f.ecdsaKeyPair.D.Bytes())
}

// UserSign assists in authenticating to Upspin servers.
func (f Factotum) UserSign(hash []byte) (upspin.Signature, error) {
	// no logging or constraining hash, because will change soon to TokenBinding anyway
	r, s, err := ecdsa.Sign(rand.Reader, &f.ecdsaKeyPair, hash)
	if err != nil {
		return sig0, err
	}
	return upspin.Signature{r, s}, nil
}

// VerHash provides the basis for signing and verifying files.
func VerHash(ciphersuite upspin.Packing, pathname upspin.PathName, time upspin.Time, dkey, cipherSum []byte) []byte {
	b := sha256.Sum256([]byte(fmt.Sprintf("%02x:%s:%d:%x:%x", ciphersuite, pathname, time, dkey, cipherSum)))
	return b[:]
}
