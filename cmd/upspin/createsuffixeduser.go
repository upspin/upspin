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
	"path/filepath"
	"text/template"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/key/keygen"
	"upspin.io/pack"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) createsuffixeduser(args ...string) {
	const help = `
Createsuffixeduser creates a suffixed user of the current user, adding it
to the keyserver and creating a new config file and keys. It takes one
argument, the suffix. The config file will be in the same directory as
the current config file and will be named config.<suffix>. Default values
for servers and packing will be taken from the current config.
`
	fs := flag.NewFlagSet("suffixed", flag.ExitOnError)
	var (
		force       = fs.Bool("force", false, "if suffixed user already exists, overwrite its keys and config file")
		dirServer   = fs.String("dir", s.Config.DirEndpoint().String(), "Directory server `address`")
		storeServer = fs.String("store", s.Config.StoreEndpoint().String(), "Store server `address`")
		bothServer  = fs.String("server", "", "Store and Directory server `address` (if combined)")
		curve       = fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
		rotate      = fs.Bool("rotate", false, "back up the existing keys and replace them with new ones")
		secrets     = fs.String("secrets", "", "`directory` to store key pair")
		secretseed  = fs.String("secretseed", "", "the seed containing a 128 bit secret in proquint format or a file that contains it")
	)
	s.ParseFlags(fs, args, help, "createsuffixeduser <suffix>")

	if fs.NArg() != 1 {
		usageAndExit(fs)
	}
	suffix := fs.Arg(0)

	if *bothServer != "" {
		if *dirServer != s.Config.DirEndpoint().String() || *storeServer != s.Config.StoreEndpoint().String() {
			usageAndExit(fs)
		}
		*dirServer = *bothServer
		*storeServer = *bothServer
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
	keyEndpoint := s.Config.KeyEndpoint()

	// We can't suffix from a suffixed user.
	uname, olsSuffix, domain, err := user.Parse(s.Config.UserName())
	if err != nil {
		s.Exit(err)
	}
	if olsSuffix != "" {
		s.Exitf("cannot add a suffix to a user with a suffix: %s", s.Config.UserName())
	}
	userName := upspin.UserName(fmt.Sprintf("%s+%s@%s", uname, suffix, domain))

	// Don't recreate a preexisting suffixed user unless forced to.
	keyServer := s.KeyServer()
	if _, err := keyServer.Lookup(userName); err == nil && !*force {
		s.Exitf("user %s already exists, use -force to recreate", userName)
	}

	cd := xData{
		UserName:  userName,
		Key:       &keyEndpoint,
		Store:     storeEndpoint,
		Dir:       dirEndpoint,
		Packing:   pack.Lookup(s.Config.Packing()).String(),
		SecretDir: *secrets,
	}

	// Write the config file.
	var configContents bytes.Buffer
	err = xTemplate.Execute(&configContents, cd)
	if err != nil {
		s.Exit(err)
	}
	configFN := fmt.Sprintf("%s.%s", flags.Config, suffix)
	err = ioutil.WriteFile(configFN, configContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.Exit(err)
		}
		dir := filepath.Dir(configFN)
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.Exit(err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.Exit(err)
		}
		err = ioutil.WriteFile(configFN, configContents.Bytes(), 0640)
		if err != nil {
			s.Exit(err)
		}
	}

	// Generate keys.
	if *secrets == "" {
		// Use the default secrets directory if none specified.
		*secrets, err = config.DefaultSecretsDir(userName)
		if err != nil {
			os.Remove(configFN)
			s.Exit(err)
		}
	}
	var pubk, privk, proquint string
	if *secretseed == "" {
		// Generate new keys.
		pubk, privk, proquint, err = keygen.Generate(*curve)
	} else {
		// Generate from the proquint.
		pubk, privk, proquint, err = keygen.FromSecret(*curve, *secretseed)
	}
	if err != nil {
		os.Remove(configFN)
		s.Exit(err)
	}
	err = keygen.SaveKeys(*secrets, *rotate, pubk, privk, proquint)
	if err != nil {
		os.Remove(configFN)
		s.Exit(err)
	}

	// Register the user.
	user := &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{*dirEndpoint},
		Stores:    []upspin.Endpoint{*storeEndpoint},
		PublicKey: upspin.PublicKey(pubk),
	}
	if err := keyServer.Put(user); err != nil {
		os.Remove(configFN)
		os.RemoveAll(*secrets)
		s.Exit(err)
	}
	where := *secrets
	fmt.Fprintln(s.Stderr, "Upspin configuration file written to:")
	fmt.Fprintf(s.Stderr, "\t%s\n", configFN)
	fmt.Fprintln(s.Stderr, "Upspin private/public key pair written to:")
	fmt.Fprintf(s.Stderr, "\t%s\n", filepath.Join(where, "public.upspinkey"))
	fmt.Fprintf(s.Stderr, "\t%s\n", filepath.Join(where, "secret.upspinkey"))
	fmt.Fprintln(s.Stderr, "This key pair provides access to your Upspin identity and data.")
	if *secretseed == "" {
		fmt.Fprintln(s.Stderr, "If you lose the keys you can re-create them by running this command:")
		fmt.Fprintf(s.Stderr, "\tupspin keygen -curve %s -secretseed %s %s\n", *curve, *secretseed, where)
		fmt.Fprintln(s.Stderr, "Write this command down and store it in a secure, private place.")
		fmt.Fprintln(s.Stderr, "Do not share your private key or this command with anyone.")
	}
	fmt.Fprintln(s.Stderr)
}

type xData struct {
	UserName        upspin.UserName
	Key, Store, Dir *upspin.Endpoint
	Packing         string
	SecretDir       string
}

var xTemplate = template.Must(template.New("config").Parse(`
username: {{.UserName}}
keyserver: {{.Key}}
storeserver: {{.Store}}
dirserver: {{.Dir}}
packing: {{.Packing}}
{{with .SecretDir}}secrets: {{.}}
{{end}}
`))
