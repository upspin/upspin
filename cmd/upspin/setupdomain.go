// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const (
		help = `
Setupdomain generates all configuration files for a new domain (or overwrites
them) and creates a proof of domain ownership challenge. If only proof of domain
ownership is needed, set -where=/dev/null or equivalent.`
		noProquint = ""
		rcTemplate = `username: %s@%s
dirserver: remote,dir.%s
storeserver: remote,store.%s
pack: %s
secrets: %s
`
	)
	fs := flag.NewFlagSet("setupdomain", flag.ExitOnError)
	where := fs.String("where", "", "`directory` to store private configuration files; default $HOME/upspin/deploy")
	project := fs.String("project", "", "name of GCP project to use. If not using GCP, leave blank")
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")

	s.parseFlags(fs, args, help, "setupdomain <domain_name> [-where=$HOME/upspin/deploy]")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	domain := fs.Arg(0)
	if domain == "" {
		s.exitf("-domain must not be empty")
	}
	switch *curveName {
	case "p256":
	case "p384":
	case "p521":
	// ok
	default:
		s.exitf("no such curve %q", *curveName)
	}

	if *where == "" {
		home := os.Getenv("HOME")
		if len(home) == 0 {
			s.exitf("no home directory")
		}
		*where = filepath.Join(home, "upspin", "deploy")
	}

	*where = filepath.Join(*where, *project)

	dirServerPath := filepath.Join(*where, "dirserver")
	s.mkdirAllLocal(dirServerPath)
	storeServerPath := filepath.Join(*where, "storeserver")
	s.mkdirAllLocal(storeServerPath)

	// Generate keys for the dirserver and the storeserver.
	dirPublic, dirPrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	storePublic, storePrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(dirServerPath, dirPublic, dirPrivate, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(storeServerPath, storePublic, storePrivate, noProquint)
	if err != nil {
		s.exit(err)
	}

	// Extra files we need to create.
	err = ioutil.WriteFile(filepath.Join(storeServerPath, "rc"),
		[]byte(fmt.Sprintf(rcTemplate, "upspin-store", domain, domain, domain, "plain", storeServerPath)), 0600)
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(dirServerPath, "rc"),
		[]byte(fmt.Sprintf(rcTemplate, "upspin-dir", domain, domain, domain, "symm", dirServerPath)), 0600)
	if err != nil {
		s.exit(err)
	}
	var symmSecret [32]byte
	_, err = rand.Read(symmSecret[:])
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(dirServerPath, "symmsecret.upspinkey"), symmSecret[:], 0600)

	fmt.Printf("Configuration files for domain %q written to %q\n", domain, *where)

	sig, err := s.context.Factotum().UserSign([]byte(s.context.UserName()))
	if err != nil {
		s.exit(err)
	}

	fmt.Printf(`
Add the following line to %s's DNS record:

NAME	TYPE	TTL	DATA
upspin	TXT	1h	%s-%s

If there are other domain owners, simply add a number after "upspin" in the NAME field
such as "upspin0", "upspin1", etc.

Once the DNS record propagates, you will be allowed to create users for %s using "upspin user -put".
`, domain, sig.R, sig.S, domain)
}
