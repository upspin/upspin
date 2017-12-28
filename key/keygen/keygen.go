// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keygen provides functions for generating Upspin key pairs and
// writing them to files.
package keygen // import "upspin.io/key/keygen"

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/errors"
	"upspin.io/key/proquint"
	"upspin.io/pack/ee"
)

// secret represents the secret seed for a key.
// It is the byte representation of a proquint string.
//
// TODO(ehg): Consider whether to use long seeds for P521.
type secret [16]byte

func (b secret) proquint() string {
	proquints := make([]interface{}, len(b)/2)
	for i := range proquints {
		proquints[i] = proquint.Encode(binary.BigEndian.Uint16(b[2*i : 2*i+2]))
	}
	return fmt.Sprintf("%s-%s-%s-%s.%s-%s-%s-%s", proquints...)
}

func secretFromProquint(secretStr string) secret {
	var b secret
	pq := []byte(secretStr)
	for i := 0; i < len(b)/2; i++ {
		binary.BigEndian.PutUint16(b[2*i:2*i+2], proquint.Decode(pq[6*i:6*i+5]))
	}
	return b
}

// Generate generates a random key pair on the given curve.
func Generate(curveName string) (public, private, secretStr string, err error) {
	// Pick secret 128 bits.
	var b secret
	ee.GenEntropy(b[:])
	return FromSecret(curveName, b.proquint())
}

// FromSecret generates a key pair with the given curve and secret seed.
func FromSecret(curveName, secret string) (public, private, secretStr string, err error) {
	const op errors.Op = "keygen.FromSecret"
	secretStr = secret
	if !ValidSecretSeed(secretStr) {
		err := errors.Errorf("expected secret like\n"+
			"\tlusab-babad-gutih-tugad.gutuk-bisog-mudof-sakat\n"+
			"got\n\t%q", secretStr)
		return "", "", "", errors.E(op, errors.Invalid, err)
	}
	b := secretFromProquint(secretStr)
	pub, priv, err := ee.CreateKeys(curveName, b[:])
	if err != nil {
		return "", "", "", err
	}
	return string(pub), priv, secretStr, nil
}

// ValidSecretSeed reports whether a seed conforms to the proquint format.
func ValidSecretSeed(seed string) bool {
	if len(seed) != 47 {
		return false
	}

	// Check if the seed can be converted to a secret and back to the same seed.
	return seed == secretFromProquint(seed).proquint()
}

// writeKeyFile writes a single key to its file, removing the file
// beforehand if necessary due to permission errors.
// If the file's parent directory does not exist, writeKeyFile creates it.
func writeKeyFile(name, key string) error {
	// Make the directory if it does not exist.
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	// Create the file.
	const create = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	fd, err := os.OpenFile(name, create, 0400)
	if os.IsPermission(err) && os.Remove(name) == nil {
		// Create may fail if file already exists and is unwritable,
		// which is how it was created.
		fd, err = os.OpenFile(name, create, 0400)
	}
	if err != nil {
		return err
	}
	// Write the key.
	_, err = fd.WriteString(key)
	if err != nil {
		fd.Close()
		return err
	}
	return fd.Close()
}

// writeKeys saves both the public and private keys to their respective files.
// If secretStr is non-empty it is appended as a comment to the private key.
// writeKeys will overwrite any existing keys.
func writeKeys(where, publicKey, privateKey, secretStr string) error {
	if secretStr != "" {
		privateKey = strings.TrimSpace(privateKey) + " # " + secretStr + "\n"
	}
	err := writeKeyFile(filepath.Join(where, "secret.upspinkey"), privateKey)
	if err != nil {
		return err
	}
	return writeKeyFile(filepath.Join(where, "public.upspinkey"), publicKey)
}

// SaveKeys writes the provided public and private keys to the given directory,
// rotating them if requested by appending the old secret to secret2.upspinkey.
// If secretStr is non-empty it is appended as a comment to the private key.
// If rotate is false and there are existing keys, SaveKeys returns an error.
// If rotate is true and there are no existing keys, SaveKeys returns an error.
func SaveKeys(where string, rotate bool, newPublic, newPrivate, secretStr string) error {
	var (
		publicFile  = filepath.Join(where, "public.upspinkey")
		privateFile = filepath.Join(where, "secret.upspinkey")
		archiveFile = filepath.Join(where, "secret2.upspinkey")
	)

	// Read existing key pair.
	private, err := ioutil.ReadFile(privateFile)
	if os.IsNotExist(err) {
		// There is nothing to save. Did we expect there to be?
		if rotate {
			return errors.Errorf("cannot rotate keys: no prior keys exist in %s", where)
		}
		// We didn't expect key rotation, so just write the new keys.
		return writeKeys(where, newPublic, newPrivate, secretStr)
	}
	if err != nil {
		return err
	}

	// There's an existing keypair. Did we not expect it?
	if !rotate {
		return errors.Errorf("prior keys exist in %s; rerun with rotate command to update keys", where)
	}

	public, err := ioutil.ReadFile(publicFile)
	if err != nil {
		return err // Halt. Existing files are corrupted and need manual attention.
	}
	if string(public) == newPublic && string(private) == newPrivate {
		return nil // No need to save duplicates.
	}

	// Write old key pair to archive file.
	archive, err := os.OpenFile(archiveFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err // We don't have permission to archive old keys?
	}

	var modtime string
	info, err := os.Stat(privateFile)
	if err != nil {
		modtime = ""
	} else {
		modtime = info.ModTime().UTC().Format(" 2006-01-02 15:04:05Z")
	}
	_, err = fmt.Fprintf(archive, "# EE%s\n%s%s", modtime, public, private)
	if err != nil {
		return err
	}
	err = archive.Close()
	if err != nil {
		return err
	}

	// Write the new keys.
	return writeKeys(where, newPublic, newPrivate, secretStr)
}
