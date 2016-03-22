// Package keyloader loads public and private keys from the user's home directory.
package keyloader

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	noKeysFound  = "no keys found"
	keyloaderErr = "keyloader: %v"
)

var (
	errNilContext = errors.New("nil context")
	zeroPrivKey   upspin.KeyPair
	zeroPubKey    upspin.PublicKey
)

// Load reads a key pair from the user's .ssh directory and loads
// them into the context.
func Load(context *upspin.Context) error {
	if context == nil {
		return errNilContext
	}
	k, err := privateKey()
	context.KeyPair = k
	return err
}

// ParsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func ParsePrivateKey(publicKey *ecdsa.PublicKey, privateKey upspin.PrivateKey) (priv *ecdsa.PrivateKey, err error) {
	n := len(privateKey) - 1
	if n > 0 && privateKey[n] == '\n' {
		privateKey = privateKey[:n]
	}
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

// publicKey returns the public key of the current user by reading from $HOME/.ssh/.
func publicKey() (upspin.PublicKey, error) {
	f, err := os.Open(filepath.Join(sshdir(), "public.upspinkey"))
	if err != nil {
		return zeroPubKey, fmt.Errorf(noKeysFound)
	}
	defer f.Close()
	buf := make([]byte, 400) // enough for p521
	n, err := f.Read(buf)
	if err != nil {
		return zeroPubKey, fmt.Errorf(keyloaderErr, err)
	}
	return upspin.PublicKey(string(buf[:n])), nil
}

// privateKey returns the private key of the current user by reading from $HOME/.ssh/.
func privateKey() (upspin.KeyPair, error) {
	f, err := os.Open(filepath.Join(sshdir(), "secret.upspinkey"))
	if err != nil {
		return zeroPrivKey, fmt.Errorf(noKeysFound)
	}
	defer f.Close()
	buf := make([]byte, 200) // enough for p521
	n, err := f.Read(buf)
	if err != nil {
		return zeroPrivKey, fmt.Errorf(keyloaderErr, err)
	}
	if buf[n-1] == '\n' {
		n--
	}
	buf = buf[:n]
	pubkey, err := publicKey()
	if err != nil {
		return zeroPrivKey, err
	}
	return upspin.KeyPair{
		Public:  pubkey,
		Private: upspin.PrivateKey(string(buf)),
	}, nil
	// TODO sanity check that Private is consistent with Public
}

func sshdir() string {
	home := os.Getenv("HOME")
	if len(home) == 0 {
		panic("no home directory")
	}
	return filepath.Join(home, ".ssh")
}
