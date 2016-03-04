// Package keyloader loads public and private keys from the user's home directory.
package keyloader

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	noKeysFound  = "no keys found for packing %d"
	keyloaderErr = "keyloader: packing %d: %v"
)

var (
	errNilContext = errors.New("nil context")
	zeroPrivKey   upspin.PrivateKey
	zeroPubKey    upspin.PublicKey
)

// Load reads a pair of keys from the user's .ssh directory and loads
// them into the context. It will load the keys according to the packing
// preference set in the context.
func Load(context *upspin.Context) error {
	if context == nil {
		return errNilContext
	}
	k, err := privateKey(context.Packing)
	context.PrivateKey = k
	return err
}

// publicKey returns the public key of the current user by reading the packing-specific key file from $HOME/.ssh/.
func publicKey(packing upspin.Packing) (upspin.PublicKey, error) {
	f, err := os.Open(filepath.Join(sshdir(), fmt.Sprintf("public.%d.upspinkey", packing)))
	if err != nil {
		return zeroPubKey, fmt.Errorf(noKeysFound, packing)
	}
	defer f.Close()
	buf := make([]byte, 400) // enough for p-521
	n, err := f.Read(buf)
	if err != nil {
		return zeroPubKey, fmt.Errorf(keyloaderErr, packing, err)
	}
	buf = buf[:n]
	return upspin.PublicKey(buf), nil
}

// privateKey returns the private key of the current user by reading the packing-specific key file from $HOME/.ssh/.
func privateKey(packing upspin.Packing) (upspin.PrivateKey, error) {
	f, err := os.Open(filepath.Join(sshdir(), fmt.Sprintf("secret.%d.upspinkey", packing)))
	if err != nil {
		return zeroPrivKey, fmt.Errorf(noKeysFound, packing)
	}
	defer f.Close()
	buf := make([]byte, 200) // big enough for P-521
	n, err := f.Read(buf)
	if err != nil {
		return zeroPrivKey, fmt.Errorf(keyloaderErr, packing, err)
	}
	if buf[n-1] == '\n' {
		n--
	}
	buf = buf[:n]
	pubkey, err := publicKey(packing)
	if err != nil {
		return zeroPrivKey, err
	}
	return upspin.PrivateKey{
		Public:  pubkey,
		Private: buf,
	}, nil
}

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		panic("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
