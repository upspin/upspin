// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/upspin"
)

func (s *State) setupserver(args ...string) {
	const (
		help = `
Setupserver
`
	)
	fs := flag.NewFlagSet("setupserver", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	s.parseFlags(fs, args, help, "setupserver [-where=$HOME/upspin/deploy] <domain>")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	domain := fs.Arg(0)

	cfgPath := filepath.Join(*where, domain)
	cfg := s.readServerConfig(cfgPath)

	// TODO: Check whether the TXT record for this domain is in place.

	// Put the server users to the key server.
	userFile, err := writeUserFile(cfgPath, cfg)
	if err != nil {
		s.exit(err)
	}
	s.user("-put", "-in", userFile)
	os.Remove(userFile)
	fmt.Printf("Successfully put %q to the key server.\n", cfg.User)

	// Put server config to the remote upspinserver.
	s.configureServer(cfgPath, cfg)

	// Set up writers.
	writers := []string{"-where=" + *where, string(s.config.UserName()), string(cfg.User)}
	// TODO(adg): add -writers flag
	s.setupwriters(writers...)

	// Check that the current configuration points to our new server.
	// If not, ask the user to change it and update the key server.
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   cfg.Addr,
	}
	if s.config.DirEndpoint() != ep || s.config.StoreEndpoint() != ep {
		fmt.Printf("Your current configuration in %q has these values:\n", flags.Config)
		fmt.Printf("\tdirserver: %v\tstoreserver: %v\n\n", s.config.DirEndpoint(), s.config.StoreEndpoint())
		fmt.Printf("To use the server we are setting up now, these values should be\n")
		fmt.Printf("\tdirserver: %v\tstoreserver: %v\n\n", ep, ep)
		return
	}

	// Make the current user root.
	s.mkdir(string(s.config.UserName()))
}

// writeUserFile reads the config file for the given server config and writes a
// YAML-encoded upspin.User to userFile. It also returns the username.
func writeUserFile(cfgPath string, cfg *ServerConfig) (userFile string, err error) {
	endpoint := []upspin.Endpoint{{
		Transport: upspin.Remote,
		NetAddr:   cfg.Addr,
	}}
	fact, err := factotum.NewFromDir(cfgPath)
	if err != nil {
		return "", err
	}
	u := upspin.User{
		Name:      cfg.User,
		Dirs:      endpoint,
		Stores:    endpoint,
		PublicKey: fact.PublicKey(),
	}
	b, err := yaml.Marshal(u)
	if err != nil {
		return "", err
	}
	f, err := ioutil.TempFile("", "setupdomain-user")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

var configureServerFiles = []string{
	"public.upspinkey",
	"secret.upspinkey",
	"serverconfig.json",
	"serviceaccount.json",
	"symmsecret.upspinkey",
}

func (s *State) configureServer(cfgPath string, cfg *ServerConfig) {
	files := map[string][]byte{}
	for _, name := range configureServerFiles {
		b, err := ioutil.ReadFile(filepath.Join(cfgPath, name))
		if err != nil {
			s.exit(err)
		}
		files[name] = b
	}
	b, err := json.Marshal(files)
	if err != nil {
		s.exit(err)
	}

	u := "https://" + string(cfg.Addr) + "/serversetup"
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(b))
	if err != nil {
		s.exit(err)
	}
	b, _ = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.exitf("upspinserver returned status %v:\n%s", resp.Status, b)
	}

	// TODO(adg): wait for server to come back online
}
