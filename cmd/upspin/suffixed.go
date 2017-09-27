// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/pack"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) suffixed(args ...string) {
	const help = `
Suffixed creates a suffixed user of the current user adding it
to the keyserver and creating a new config file, keys, and 
(optionally) self-signed certs. Everything is placed in a new
directory with the same name as the suffixed user. Unless
overridden by flags, config setting are the same as for the
current user.
`
	fs := flag.NewFlagSet("suffixed", flag.ExitOnError)
	var (
		force       = fs.Bool("force", false, "create a new user even if keys and config file exist")
		dirServer   = fs.String("dir", "", "Directory server `address`")
		storeServer = fs.String("store", "", "Store server `address`")
		bothServer  = fs.String("server", "", "Store and Directory server `address` (if combined)")
		curve       = fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
		secrets     = fs.String("secrets", "", "`directory` to store key pair")
		secretseed  = fs.String("secretseed", "", "the seed containing a 128 bit secret in proquint format or a file that contains it")
	)
	s.ParseFlags(fs, args, help, "suffixed suffix")

	if fs.NArg() != 1 {
		usageAndExit(fs)
	}

	if *bothServer != "" {
		*dirServer = *bothServer
		*storeServer = *bothServer
	}
	if *dirServer == "" {
		*dirServer = s.Config.DirEndpoint().String()
	}
	if *storeServer == "" {
		*storeServer = s.Config.StoreEndpoint().String()
	}

	// Parse -dir and -store flags as addresses and construct remote endpoints.
	dirEndpoint, err := parseAddress(*dirServer)
	if err != nil {
		s.Exitf("error parsing -dir=%q: %v", dirServer, err)
	}
	storeEndpoint, err := parseAddress(*storeServer)
	if err != nil {
		s.Exitf("error parsing -store=%q: %v", storeServer, err)
	}

	// We can't suffix from a suffixed user.
	uname, suffix, domain, err := user.Parse(s.Config.UserName())
	if err != nil {
		s.Exit(err)
	}
	if suffix != "" {
		s.Exitf("cannot suffix a suffixed user: %s", s.Config.UserName())
	}
	userName := upspin.UserName(fmt.Sprintf("%s+%s@%s", uname, fs.Arg(0), domain))

	// Don't recreate a preexisting suffixed user unless forced to.
	keyServer := s.KeyServer()
	if _, err := keyServer.Lookup(userName); err == nil && !*force {
		s.Exitf("user %s already exists", userName)
	}

	// Default all files to the same directory.
	defaultDir := string(userName)
	if *secrets == "" {
		*secrets = defaultDir
	}

	cd := configData{
		UserName:  userName,
		Store:     storeEndpoint,
		Dir:       dirEndpoint,
		Packing:   pack.Lookup(s.Config.Packing()).String(),
		SecretDir: *secrets,
	}

	// Write the config file.
	var configContents bytes.Buffer
	err = configTemplate.Execute(&configContents, cd)
	if err != nil {
		s.Exit(err)
	}
	configFN := path.Join(defaultDir, "config")
	fmt.Printf("creating config file %s\n", configFN)
	err = ioutil.WriteFile(configFN, configContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.Exitf("cannot create %s: %v", configFN, err)
		}
		dir := defaultDir
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.Exitf("cannot create %s: %v", flags.Config, err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.Exitf("cannot make directory %s: %v", dir, mkdirErr)
		}
		err = ioutil.WriteFile(configFN, configContents.Bytes(), 0640)
		if err != nil {
			s.Exit(err)
		}
	}

	// Generate a new key.
	s.keygenCommand(*secrets, *curve, *secretseed, false)

	// Read the config file. We will need a Factotum bound to this user.
	suffixedConfig, err := config.FromFile(configFN)
	if err != nil {
		s.Exit(err)
	}
	pk := suffixedConfig.Factotum().PublicKey()

	// Register the user.
	user := &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{*dirEndpoint},
		Stores:    []upspin.Endpoint{*storeEndpoint},
		PublicKey: pk,
	}
	if err := keyServer.Put(user); err != nil {
		s.Exit(err)
	}
}
