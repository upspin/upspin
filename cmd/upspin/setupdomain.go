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

	"upspin.io/flags"
)

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const (
		help = `
Setupdomain generates all configuration files for a new domain (or overwrites
them) and creates a proof of domain ownership challenge.

If using Google Cloud Platform (GCP), the project name must be specified with
-project, so that cmd/upspin-deploy can find the config files.

If only proof of domain ownership is needed, set -where="".

Once the domain has been set up and its servers deployed, use setupwriters to
set access controls.
`
	)
	fs := flag.NewFlagSet("setupdomain", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	s.parseFlags(fs, args, help, "[-project=<gcp_project_name>] setupdomain [-where=$HOME/upspin/deploy] <domain_name>")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	domain := fs.Arg(0)
	if domain == "" {
		s.exitf("domain must be provided")
	}
	switch *curveName {
	case "p256":
	case "p384":
	case "p521":
	// ok
	default:
		s.exitf("no such curve %q", *curveName)
	}

	dstDir := *where
	if dstDir == "" {
		tmpDir, err := ioutil.TempDir("", "setupdomain-")
		if err != nil {
			s.exit(err)
		}
		defer os.RemoveAll(tmpDir)
		dstDir = tmpDir
	}

	dstDir = filepath.Join(dstDir, flags.Project)

	dirServerPath := filepath.Join(dstDir, "dirserver")
	s.mkdirAllLocal(dirServerPath)
	storeServerPath := filepath.Join(dstDir, "storeserver")
	s.mkdirAllLocal(storeServerPath)

	// Generate keys for the dirserver and the storeserver.
	var noProquint string
	dirPublic, dirPrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	storePublic, storePrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(dirServerPath, dirPublic, dirPrivate)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(storeServerPath, storePublic, storePrivate)
	if err != nil {
		s.exit(err)
	}

	// Extra files we need to create.
	err = ioutil.WriteFile(filepath.Join(storeServerPath, "rc"),
		[]byte(fmt.Sprintf(rcFormat, "upspin-store", domain, domain, domain, "plain", storeServerPath)), 0600)
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(dirServerPath, "rc"),
		[]byte(fmt.Sprintf(rcFormat, "upspin-dir", domain, domain, domain, "symm", dirServerPath)), 0600)
	if err != nil {
		s.exit(err)
	}
	var symmSecret [32]byte
	_, err = rand.Read(symmSecret[:])
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(dirServerPath, "symmsecret.upspinkey"), symmSecret[:], 0600)

	if *where != "" {
		fmt.Printf("Configuration files for domain %q written to %q\n", domain, dstDir)
	}
	msg := "upspin-domain:" + domain + "-" + string(s.context.UserName())
	sig, err := s.context.Factotum().Sign([]byte(msg))
	if err != nil {
		s.exit(err)
	}

	fmt.Printf(dnsMessageFormat, domain, sig.R, sig.S, domain)
}

const (
	rcFormat = `username: %s@%s
dirserver: remote,dir.%s
storeserver: remote,store.%s
packing: %s
secrets: %s
`
	dnsMessageFormat = `
Add the following line to %s's DNS record:

NAME	TYPE	TTL	DATA
@	TXT	1h	upspin:%x-%x

If there are other domain owners, simply add the entry above.

Once the DNS record propagates, you will be allowed to create users for %s using "upspin user -put".
`
)
