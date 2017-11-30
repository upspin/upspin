// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file contains the implementation of the keygen command.

import (
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"upspin.io/errors"
	"upspin.io/key/keygen"
	"upspin.io/subcmd"
)

func (s *State) keygen(args ...string) {
	const help = `
Keygen creates a new Upspin key pair and stores the pair in local files
secret.upspinkey and public.upspinkey in the specified directory.
Existing key pairs are appended to secret2.upspinkey.
Keygen does not update the information in the key server;
use the "user -put" command for that.

New users should instead use the "signup" command to create their first key.

See the description for rotate for information about updating keys.
`
	// Keep flags in sync with signup.go. New flags here should appear
	// there as well.
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	var (
		curve      = fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
		secretSeed = fs.String("secretseed", "", "the seed containing a 128-bit secret in proquint format or a file that contains it")
		rotate     = fs.Bool("rotate", false, "back up the existing keys and replace them with new ones")
	)
	s.ParseFlags(fs, args, help, "keygen [-curve=256] [-secretseed=seed] <directory>")
	if fs.NArg() != 1 {
		usageAndExit(fs)
	}
	s.keygenCommand(fs.Arg(0), *curve, *secretSeed, *rotate)
}

func (s *State) keygenCommand(where, curve, secretseed string, rotate bool) {
	switch curve {
	case "p256", "p384", "p521":
		// ok
	default:
		s.Exitf("no such curve %q", curve)
	}

	public, private, secretStr, err := s.createKeys(curve, secretseed)
	if err != nil {
		s.Exitf("creating keys: %v", err)
	}

	err = keygen.SaveKeys(where, rotate, public, private, secretStr)
	if err != nil {
		s.Exitf("keys not generated: %s", err)
	}
	if rotate {
		archiveFile := filepath.Join(where, "secret2.upspinkey")
		fmt.Fprintf(s.Stderr, "Saved previous key pair to:\n\t%s\n", archiveFile)
	}

	fmt.Fprintln(s.Stderr, "Upspin private/public key pair written to:")
	fmt.Fprintf(s.Stderr, "\t%s\n", filepath.Join(where, "public.upspinkey"))
	fmt.Fprintf(s.Stderr, "\t%s\n", filepath.Join(where, "secret.upspinkey"))
	fmt.Fprintln(s.Stderr, "This key pair provides access to your Upspin identity and data.")
	if secretseed == "" {
		fmt.Fprintln(s.Stderr, "If you lose the keys you can re-create them by running this command:")
		fmt.Fprintf(s.Stderr, "\tupspin keygen -curve %s -secretseed %s %s\n", curve, secretStr, where)
		fmt.Fprintln(s.Stderr, "Write this command down and store it in a secure, private place.")
		fmt.Fprintln(s.Stderr, "Do not share your private key or this command with anyone.")
	}
	if rotate {
		fmt.Fprintln(s.Stderr, "\nTo install new keys in the key server, see 'upspin rotate -help'.")
	}
	fmt.Fprintln(s.Stderr)
}

func (s *State) createKeys(curveName, secretFlag string) (public, private, secretStr string, err error) {
	// There are three cases:
	// 1) No secretFlag was given. Create a new secret seed.
	// 2) A secretFlag looks valid. Accept it.
	// 3) The secretFlag must be a file. Try to read it.
	if secretFlag == "" {
		return keygen.Generate(curveName)
	}
	if !keygen.ValidSecretSeed(secretFlag) {
		data, err := ioutil.ReadFile(subcmd.Tilde(secretFlag))
		if err != nil {
			return "", "", "", errors.E(errors.Op("keygen"), errors.IO, err)
		}
		secretFlag = strings.TrimSpace(string(data))
	}
	return keygen.FromSecret(curveName, secretFlag)
}
