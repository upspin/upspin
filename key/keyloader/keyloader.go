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
		Private: string(buf),
	}, nil
	// TODO sanity check that Private is consistent with Public
}

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		panic("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
