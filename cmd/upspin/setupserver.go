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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/bind"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/upspin"
)

func (s *State) setupserver(args ...string) {
	const (
		help = `
Setupserver is the final step of setting up an upspinserver.
It assumes that you have run 'setupdomain' and 'setupstorage'.

It registers the user created by 'setupdomain' domain with the key server,
copies the configuration files from $HOME/upspin/deploy/example.com to the
upspinserver and restarts it, puts the Writers file, and makes the root for the
calling user.

The calling user and the server user are given write access to the store and
directory by default. You may specify additional writers with the -writers
flag. For instance, if you want all users @example.com to be able to access
storage, specify "-writers=*@example.com".

The calling user must be the same one that ran 'upspin setupdomain'.
`
	)
	fs := flag.NewFlagSet("setupserver", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := fs.String("domain", "", "domain `name` for this Upspin installation")
	host := fs.String("host", "", "host `name` of upspinserver (empty implies the cluster dir.domain and store.domain)")
	writers := fs.String("writers", "", "additional `users` to be given write access to this server")
	s.parseFlags(fs, args, help, "setupserver -domain=<domain> -host=<host> [-where=$HOME/upspin/deploy] [-writers=user,...]")
	if *domain == "" || *host == "" {
		s.failf("the -domain and -host flags must be provided")
		fs.Usage()
	}

	cfgPath := filepath.Join(*where, *domain)
	cfg := s.readServerConfig(cfgPath)

	if !strings.Contains(*host, ":") {
		*host += ":443"
	}
	_, _, err := net.SplitHostPort(*host)
	if err != nil {
		s.exitf("invalid -host argument %q: %v", *host, err)
	}
	cfg.Addr = upspin.NetAddr(*host)

	s.writeServerConfig(cfgPath, cfg)

	// Put the server user to the key server.
	key, err := bind.KeyServer(s.config, s.config.KeyEndpoint())
	if err != nil {
		s.exit(err)
	}
	local, err := userFor(cfgPath, cfg)
	if err != nil {
		s.exit(err)
	}
	remote, err := key.Lookup(cfg.User)
	if err == nil {
		// TODO(adg): compare local and remote for discrepancies.
		_ = remote
		fmt.Printf("User %q already exists on key server.\n", cfg.User)
	} else {
		if err := key.Put(local); err != nil {
			// TODO(adg): Check whether the TXT record for this
			// domain is in place.
			s.exit(err)
		}
		fmt.Printf("Successfully put %q to the key server.\n", cfg.User)
	}

	// Create Writers file.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s\n%s\n", s.config.UserName(), cfg.User)
	for _, user := range strings.Split(*writers, ",") {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		fmt.Fprintf(&buf, "%s\n", user)
	}
	if err := ioutil.WriteFile(filepath.Join(cfgPath, "Writers"), buf.Bytes(), 0644); err != nil {
		s.exit(err)
	}

	// Put server config to the remote upspinserver.
	s.configureServer(cfgPath, cfg)
	fmt.Printf("Configured upspinserver at %q.\n", cfg.Addr)

	// Check that the current configuration points to our new server.
	// If not, ask the user to change it and update the key server.
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   cfg.Addr,
	}
	if s.config.DirEndpoint() != ep || s.config.StoreEndpoint() != ep {
		fmt.Printf("Your current configuration in %q has these values:\n", flags.Config)
		fmt.Printf("\tdirserver: %v\n\tstoreserver: %v\n\n", s.config.DirEndpoint(), s.config.StoreEndpoint())
		fmt.Printf("To use the server we are setting up now, these values should be\n")
		fmt.Printf("\tdirserver: %v\n\tstoreserver: %v\n\n", ep, ep)
		return
	}

	// Make the current user root.
	root := string(s.config.UserName())
	s.mkdir(root)
	fmt.Printf("Created root %q.\n", root)
}

func userFor(cfgPath string, cfg *ServerConfig) (*upspin.User, error) {
	ep := []upspin.Endpoint{{
		Transport: upspin.Remote,
		NetAddr:   cfg.Addr,
	}}
	fact, err := factotum.NewFromDir(cfgPath)
	if err != nil {
		return nil, err
	}
	return &upspin.User{
		Name:      cfg.User,
		Dirs:      ep,
		Stores:    ep,
		PublicKey: fact.PublicKey(),
	}, nil
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

	u := "https://" + string(cfg.Addr) + "/setupserver"
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(b))
	if err != nil {
		s.exit(err)
	}
	b, _ = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.exitf("upspinserver returned status %v:\n%s", resp.Status, b)
	}
}

func (s *State) readServerConfig(cfgPath string) *ServerConfig {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.exitf("No server config file found at %q.\nRun 'upspin setupdomain' first.", cfgFile)
		}
		s.exit(err)
	}
	cfg := &ServerConfig{}
	if err := json.Unmarshal(b, cfg); err != nil {
		s.exit(err)
	}
	return cfg
}

func (s *State) writeServerConfig(cfgPath string, cfg *ServerConfig) {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
	b, err := json.Marshal(cfg)
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(cfgFile, b, 0644)
	if err != nil {
		s.exit(err)
	}
}

// Keep the following declarations in sync with cmd/upspinserver/main.go.
// TODO(adg): move these to their own package if/when there are more users.

type ServerConfig struct {
	Addr   upspin.NetAddr
	User   upspin.UserName
	Bucket string
}

const serverConfigFile = "serverconfig.json"

var configureServerFiles = []string{
	"Writers",
	"public.upspinkey",
	"secret.upspinkey",
	"serverconfig.json",
	"serviceaccount.json",
	"symmsecret.upspinkey",
}
