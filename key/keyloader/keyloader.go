// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keyloader loads public and private keys from the user's home directory.
package keyloader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/factotum"
	"upspin.io/upspin"
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
	if err != nil {
		return err
	}
	context.Factotum, err = factotum.New(k)
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
	buf = buf[:n]
	buf = []byte(strings.TrimSpace(string(buf)))
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
