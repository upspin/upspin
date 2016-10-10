// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keygen creates local files secret.upspinkey and public.upspinkey in ~/.ssh
// which contain the private and public parts of a keypair.
// Existing keypairs are appended to ~/.ssh/secret2.upspinkey.
// Someday we hope to integrate with ssh-agent.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"upspin.io/cmd/keygen/proquint"
	"upspin.io/errors"
	"upspin.io/pack/ee"
)

var (
	curveName = flag.String("curve", "p256", "curve name: p256, p384, or p521")
	secret    = flag.String("secretseed", "", "128 bit secret seed in proquint format")
	where     = flag.String("where", "", "directory to write keys. If empty, $HOME/.ssh/")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	flag.Parse()
	switch *curveName {
	case "p256":
	case "p384":
	case "p521":
		// ok
	default:
		log.Printf("no such curve %q", *curveName)
		flag.Usage()
		os.Exit(2)
	}

	public, private, proquintStr, err := createKeys(*curveName, *secret)
	if err != nil {
		log.Fatalf("creating keys: %v", err)
	}
	err = saveKeys(keydir())
	if err != nil {
		log.Fatalf("saving previous keys: %v", err)
	}
	err = writeKeys(keydir(), public, private, proquintStr)
	if err != nil {
		log.Fatalf("writing keys: %v", err)
	}
}

func createKeys(curveName, secret string) (public string, private, proquintStr string, err error) {
	// Pick secret 128 bits.
	// TODO(ehg)  Consider whether we are willing to ask users to write long seeds for P521.
	b := make([]byte, 16)
	if len(secret) > 0 {
		if len((secret)) != 47 || (secret)[5] != '-' {
			return "", "", "", err
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
		proquintStr = fmt.Sprintf("-secretseed %s-%s-%s-%s.%s-%s-%s-%s\n", proquints...)
		// Ignore punctuation on input;  this format is just to help the user keep their place.

	}

	pub, priv, err := ee.CreateKeys(curveName, b)
	if err != nil {
		return "", "", "", err
	}
	return string(pub), priv, proquintStr, nil
}

func writeKeys(where, publicKey, privateKey, proquintStr string) error {
	// Save the keys to files.
	fdPrivate, err := os.Create(filepath.Join(where, "secret.upspinkey"))
	if err != nil {
		return err
	}
	err = fdPrivate.Chmod(0600)
	if err != nil {
		return err
	}
	fdPublic, err := os.Create(filepath.Join(where, "public.upspinkey"))
	if err != nil {
		return err
	}

	fdPrivate.WriteString(privateKey)
	fdPublic.WriteString(string(publicKey))

	err = fdPrivate.Close()
	if err != nil {
		return err
	}
	err = fdPublic.Close()
	if err != nil {
		return err
	}
	if proquintStr != "" {
		fmt.Print(proquintStr)
	}
	return nil
}

func saveKeys(where string) error {
	private, err := os.Open(filepath.Join(where, "secret.upspinkey"))
	if os.IsNotExist(err) {
		return nil // There is nothing we need to save.
	}
	priv, err := ioutil.ReadAll(private)
	if err != nil {
		return err
	}
	public, err := os.Open(filepath.Join(where, "public.upspinkey"))
	if err != nil {
		return err // Halt. Existing files are corrupted and need manual attention.
	}
	pub, err := ioutil.ReadAll(public)
	if err != nil {
		return err
	}
	archive, err := os.OpenFile(filepath.Join(where, "secret2.upspinkey"),
		os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err // We don't have permission to archive old keys?
	}
	_, err = archive.Write([]byte("# EE \n")) // TODO(ehg) add file date
	if err != nil {
		return err
	}
	_, err = archive.Write(pub)
	if err != nil {
		return err
	}
	_, err = archive.Write(priv)
	if err != nil {
		return err
	}
	return nil
}

func keydir() string {
	if where != nil && len(*where) > 0 {
		return *where
	}
	home := os.Getenv("HOME")
	if len(home) == 0 {
		log.Fatal("no home directory")
	}
	return filepath.Join(home, ".ssh")
}
