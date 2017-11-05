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
	"path/filepath"
	"strings"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/subcmd"
	"upspin.io/upspin"
)

func (s *State) setupserver(args ...string) {
	const (
		help = `
Setupserver is the final step of setting up an upspinserver.
It assumes that you have run 'setupdomain' and (optionally) 'setupstorage'.

It registers the user created by 'setupdomain' domain with the key server,
copies the configuration files from $where/$domain to the upspinserver and
restarts it, puts the Writers file, and makes the root for the calling user.

The calling user and the server user are included in the Writers file by
default (giving them write access to the store and directory). You may specify
additional writers with the -writers flag. For instance, if you want all users
@example.com to be able to access storage, specify "-writers=*@example.com".

The calling user must be the same one that ran 'upspin setupdomain'.
`
	)
	fs := flag.NewFlagSet("setupserver", flag.ExitOnError)
	where := fs.String("where", filepath.Join(config.Home(), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := fs.String("domain", "", "domain `name` for this Upspin installation")
	host := fs.String("host", "", "host `name` of upspinserver (empty implies the cluster dir.domain and store.domain)")
	writers := fs.String("writers", "", "additional `users` to be given write access to this server")
	s.ParseFlags(fs, args, help, "setupserver -domain=<domain> -host=<host> [-where=$HOME/upspin/deploy] [-writers=user,...]")
	if *domain == "" || *host == "" {
		s.Failf("the -domain and -host flags must be provided")
		usageAndExit(fs)
	}

	cfgPath := filepath.Join(subcmd.Tilde(*where), *domain)
	cfg := s.ReadServerConfig(cfgPath)

	// Stash the provided host name in the server config file.
	if !strings.Contains(*host, ":") {
		*host += ":443"
	}
	_, _, err := net.SplitHostPort(*host)
	if err != nil {
		s.Exitf("invalid -host argument %q: %v", *host, err)
	}
	cfg.Addr = upspin.NetAddr(*host)
	s.WriteServerConfig(cfgPath, cfg)

	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   cfg.Addr,
	}

	// Put the server user to the key server.
	key, err := bind.KeyServer(s.Config, s.Config.KeyEndpoint())
	if err != nil {
		s.Exit(err)
	}
	local, err := userFor(cfgPath, cfg)
	if err != nil {
		s.Exit(err)
	}
	remote, err := key.Lookup(cfg.User)
	if err == nil {
		// TODO(adg): compare local and remote for discrepancies.
		_ = remote
		fmt.Fprintf(s.Stderr, "User %q already exists on key server.\n", cfg.User)
	} else {
		if err := key.Put(local); err != nil {
			// TODO(adg): Check whether the TXT record for this
			// domain is in place.
			s.Exit(err)
		}
		fmt.Fprintf(s.Stderr, "Successfully put %q to the key server.\n", cfg.User)
	}

	// Create Writers file.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s\n%s\n", s.Config.UserName(), cfg.User)
	for _, user := range strings.Split(*writers, ",") {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		fmt.Fprintf(&buf, "%s\n", user)
	}
	if err := ioutil.WriteFile(filepath.Join(cfgPath, "Writers"), buf.Bytes(), 0644); err != nil {
		s.Exit(err)
	}

	// Write Upspin config file for the server user,
	// for convenience when acting as the server user.
	configFile := filepath.Join(cfgPath, "config")
	configBody := new(bytes.Buffer)
	if err := configTemplate.Execute(configBody, configData{
		UserName:  cfg.User,
		Store:     &ep,
		Dir:       &ep,
		SecretDir: cfgPath,
		Packing:   "ee",
	}); err != nil {
		s.Exit(err)
	}
	if err := ioutil.WriteFile(configFile, configBody.Bytes(), 0644); err != nil {
		s.Exit(err)
	}

	// Put server config to the remote upspinserver.
	s.configureServer(cfgPath, cfg)
	fmt.Fprintf(s.Stderr, "Configured upspinserver at %q.\n", cfg.Addr)

	// Check that the current configuration points to our new server.
	// If not, ask the user to change it and update the key server.
	if s.Config.DirEndpoint() != ep || s.Config.StoreEndpoint() != ep {
		fmt.Fprintf(s.Stderr, "Your current configuration in %q has these values:\n", flags.Config)
		fmt.Fprintf(s.Stderr, "\tdirserver: %v\n\tstoreserver: %v\n\n", s.Config.DirEndpoint(), s.Config.StoreEndpoint())
		fmt.Fprintf(s.Stderr, "To use the server we are setting up now, these values should be\n")
		fmt.Fprintf(s.Stderr, "\tdirserver: %v\n\tstoreserver: %v\n\n", ep, ep)
		return
	}

	// Make the current user root.
	root := string(s.Config.UserName())
	s.mkdir(root)
	fmt.Fprintf(s.Stderr, "Created root %q.\n", root)
}

func userFor(cfgPath string, cfg *subcmd.ServerConfig) (*upspin.User, error) {
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

func (s *State) configureServer(cfgPath string, cfg *subcmd.ServerConfig) {
	files := map[string][]byte{}
	for _, name := range subcmd.SetupServerFiles {
		b, err := ioutil.ReadFile(filepath.Join(cfgPath, name))
		if err != nil {
			s.Exit(err)
		}
		files[name] = b
	}
	b, err := json.Marshal(files)
	if err != nil {
		s.Exit(err)
	}

	u := "https://" + string(cfg.Addr) + "/setupserver"
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(b))
	if err != nil {
		s.Exit(err)
	}
	b, _ = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.Exitf("upspinserver returned status %v:\n%s", resp.Status, b)
	}
}
