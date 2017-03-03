// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file contains the implementation of the keygen command.

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"upspin.io/errors"
	"upspin.io/key/proquint"
	"upspin.io/pack/ee"
	"upspin.io/subcmd"
)

func (s *State) keygen(args ...string) {
	const help = `
Keygen creates a new Upspin key pair and stores the pair in local
files secret.upspinkey and public.upspinkey in $HOME/.ssh. Existing
key pairs are appended to $HOME/.ssh/secret2.upspinkey. Keygen does
not update the information in the key server; use the user -put
command for that.

New users should instead use the signup command to create their
first key. Keygen can be used to create new keys.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	fs.String("secretseed", "", "128 bit secret `seed` in proquint format")
	fs.String("where", filepath.Join(os.Getenv("HOME"), ".ssh"), "`directory` to store keys")
	fs.Bool("rotate", false, "whether to rotate existing keys and replace them with new ones")
	s.ParseFlags(fs, args, help, "keygen [-curve=256] [-secretseed=seed] [-where=$HOME/.ssh]")
	if fs.NArg() != 0 {
		fs.Usage()
	}
	s.keygenCommand(fs)
}

func (s *State) keygenCommand(fs *flag.FlagSet) {
	curve := subcmd.StringFlag(fs, "curve")
	switch curve {
	case "p256", "p384", "p521":
		// ok
	default:
		log.Printf("no such curve %q", curve)
		fs.Usage()
	}

	public, private, proquintStr, err := createKeys(curve, subcmd.StringFlag(fs, "secretseed"))
	if err != nil {
		s.Exitf("creating keys: %v", err)
	}

	where := subcmd.StringFlag(fs, "where")
	if where == "" {
		s.Exitf("-where must not be empty")
	}
	rotate := subcmd.BoolFlag(fs, "rotate")
	err = saveKeys(where, rotate)
	if err != nil {
		s.Exitf("saving previous keys failed, keys not generated: %s", err)
	}
	err = writeKeys(where, public, private)
	if err != nil {
		s.Exitf("writing keys: %v", err)
	}
	fmt.Println("Upspin private/public key pair written to:")
	fmt.Printf("\t%s\n", filepath.Join(where, "public.upspinkey"))
	fmt.Printf("\t%s\n", filepath.Join(where, "secret.upspinkey"))
	fmt.Println("This key pair provides access to your Upspin identity and data.")
	if proquintStr != "" {
		fmt.Println("If you lose the keys you can re-create them by running this command:")
		fmt.Printf("\tupspin keygen -secretseed %s\n", proquintStr)
		fmt.Println("Write this command down and store it in a secure, private place.")
		fmt.Println("Do not share your private key or this command with anyone.")
	} else {
		fmt.Println("Do not share your private key with anyone.")
	}
	if rotate {
		fmt.Println("\nTo install new keys in the key server, do 'upspin rotate' next.")
	}
	fmt.Println()
}

func createKeys(curveName, secret string) (public string, private, proquintStr string, err error) {
	// Pick secret 128 bits.
	// TODO(ehg)  Consider whether we are willing to ask users to write long seeds for P521.
	b := make([]byte, 16)
	if len(secret) > 0 {
		if len((secret)) != 47 || (secret)[5] != '-' {
			log.Printf("expected secret like\n lusab-babad-gutih-tugad.gutuk-bisog-mudof-sakat\n"+
				"not\n %s\nkey not generated", secret)
			return "", "", "", errors.E("keygen", errors.Invalid, errors.Str("bad format for secret"))
		}
		for i := 0; i < 8; i++ {
			binary.BigEndian.PutUint16(b[2*i:2*i+2], proquint.Decode([]byte((secret)[6*i:6*i+5])))
		}
	} else {
		ee.GenEntropy(b)
		proquints := make([]interface{}, 8)
		for i := 0; i < 8; i++ {
			proquints[i] = proquint.Encode(binary.BigEndian.Uint16(b[2*i : 2*i+2]))
		}
		proquintStr = fmt.Sprintf("%s-%s-%s-%s.%s-%s-%s-%s", proquints...)
		// Ignore punctuation on input;  this format is just to help the user keep their place.
	}

	pub, priv, err := ee.CreateKeys(curveName, b)
	if err != nil {
		return "", "", "", err
	}
	return string(pub), priv, proquintStr, nil
}

// writeKeyFile writes a single key to its file, removing the file
// beforehand if necessary due to permission errors.
func writeKeyFile(name, key string) error {
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
	defer fd.Close()
	_, err = fd.WriteString(key)
	return err

}

// writeKeys save both the public and private keys to their respective files.
func writeKeys(where, publicKey, privateKey string) error {
	err := writeKeyFile(filepath.Join(where, "secret.upspinkey"), privateKey)
	if err != nil {
		return err
	}
	err = writeKeyFile(filepath.Join(where, "public.upspinkey"), publicKey)
	if err != nil {
		return err
	}
	return nil
}

func saveKeys(where string, rotate bool) error {
	var (
		publicFile  = filepath.Join(where, "public.upspinkey")
		privateFile = filepath.Join(where, "secret.upspinkey")
		archiveFile = filepath.Join(where, "secret2.upspinkey")
	)

	// Read existing key pair.
	private, err := ioutil.ReadFile(privateFile)
	if os.IsNotExist(err) {
		// There is nothing we need to save. Did we expect it to be?
		if rotate {
			return errors.Str("no keys exist to rotate")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !rotate {
		return errors.Str("keys exists; -rotate must be used")
	}
	public, err := ioutil.ReadFile(publicFile)
	if err != nil {
		return err // Halt. Existing files are corrupted and need manual attention.
	}

	// Write old key pair to archive file.
	archive, err := os.OpenFile(archiveFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err // We don't have permission to archive old keys?
	}
	// TODO(ehg) add file date
	_, err = fmt.Fprintf(archive, "# EE\n%s%s", public, private)
	if err != nil {
		return err
	}
	err = archive.Close()
	if err != nil {
		return err
	}
	fmt.Printf("Successfully saved previous keys in %q", archiveFile)
	return nil
}
