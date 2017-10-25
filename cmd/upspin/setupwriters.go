// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/subcmd"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) setupwriters(args ...string) {
	const help = `
Setupwriters creates or updates the Writers file for the given domain.
The file lists the names of users granted access to write to the domain's
store server and to create their own root on the directory server.

A wildcard permits access to all users of a domain ("*@example.com").

The user name of the project's directory server is automatically included in
the list, so the directory server can use the store for its own data storage.
`
	fs := flag.NewFlagSet("setupwriters", flag.ExitOnError)
	where := fs.String("where", filepath.Join(config.Home(), "upspin", "deploy"), "`directory` containing private configuration files")
	domain := fs.String("domain", "", "domain `name` for this Upspin installation")
	s.ParseFlags(fs, args, help, "setupwriters [-where=$HOME/upspin/deploy] -domain=<domain> <user names>")

	if *where == "" {
		s.Failf("the -where flag must not be empty")
		usageAndExit(fs)
	}
	if *domain == "" {
		s.Failf("the -domain must not be empty")
		usageAndExit(fs)
	}

	var users []upspin.UserName
	for _, arg := range fs.Args() {
		u, err := user.Clean(upspin.UserName(arg))
		if err != nil {
			s.Exit(err)
		}
		users = append(users, u)
	}

	cfgDir := filepath.Join(subcmd.Tilde(*where), *domain)
	if fi, err := os.Stat(cfgDir); err != nil {
		s.Exitf("error reading configuration directory: %v", err)
	} else if !fi.IsDir() {
		s.Exitf("specified location is not a directory: %v", cfgDir)
	}

	var dirUser upspin.UserName
	storeCfg, err := config.FromFile(filepath.Join(cfgDir, "config"))
	if errors.Is(errors.NotExist, err) {
		storeCfg, err = config.FromFile(filepath.Join(cfgDir, "storeserver", "config"))
		if err != nil {
			s.Exit(err)
		}
		dirCfg, err := config.FromFile(filepath.Join(cfgDir, "dirserver", "config"))
		if err != nil {
			s.Exit(err)
		}
		// Created by setupdomain -cluster, separate users for dir and store.
		dirUser = dirCfg.UserName()
	} else if err != nil {
		s.Exit(err)
	} else {
		// Created by setupdomain -cluster=false, one user for dir and store.
		dirUser = storeCfg.UserName()
	}
	storeUser := storeCfg.UserName()

	// Act as the store user.
	c := client.New(storeCfg)

	// Make the store root.
	_, err = c.MakeDirectory(upspin.PathName(storeUser) + "/")
	if err != nil && !errors.Is(errors.Exist, err) {
		s.Exit(err)
	}
	// Make the Group directory.
	_, err = c.MakeDirectory(upspin.PathName(storeUser) + "/Group")
	if err != nil && !errors.Is(errors.Exist, err) {
		s.Exit(err)
	}

	// Prepare Access file and put it to the server.
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "*:%v\n", storeUser)
	if dirUser != storeUser {
		fmt.Fprintf(buf, "l,r:%v\n", dirUser)
	}
	_, err = c.Put(upspin.PathName(storeUser)+"/Access", buf.Bytes())
	if err != nil {
		s.Exit(err)
	}

	// Prepare Writers file and put it to the server.
	buf.Reset()
	fmt.Fprintln(buf, storeUser)
	if dirUser != storeUser {
		fmt.Fprintln(buf, dirUser)
	}
	for _, u := range users {
		fmt.Fprintln(buf, u)
	}
	_, err = c.Put(upspin.PathName(storeUser)+"/Group/Writers", buf.Bytes())
	if err != nil {
		s.Exit(err)
	}
}
