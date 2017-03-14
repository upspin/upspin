// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/factotum"
	"upspin.io/key/usercache"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) user(args ...string) {
	const help = `
User prints in YAML format the user record stored in the key server
for the specified user, by default the current user.

With the -put flag, user writes or replaces the information stored
for the current user, such as to update keys or server information.
The information is read from standard input or from the file provided
with the -in flag. The input must provide the complete record for
the user, and must be in the same YAML format printed by the command
without the -put flag.

When using -put, the command takes no arguments. The name of the
user whose record is to be updated must be provided in the input
record and must either be the current user or the name of another
user whose domain is administered by the current user.

A handy way to use the command is to edit the config file and run
	upspin user | upspin user -put

To install new users see the signup command.
`
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	put := fs.Bool("put", false, "write new user record")
	inFile := fs.String("in", "", "input file (default standard input)")
	force := fs.Bool("force", false, "force writing user record even if key is empty")
	s.ParseFlags(fs, args, help, "user [username...]\n              user -put [-in=inputfile] [-force] [username]")
	keyServer := s.KeyServer()
	if *put {
		s.putUser(fs, keyServer, s.GlobOneLocal(*inFile), *force)
		return
	}
	if *inFile != "" {
		s.Exitf("-in only available with -put")
	}
	if *force {
		s.Exitf("-force only available with -put")
	}
	var userNames []upspin.UserName
	if fs.NArg() == 0 {
		userNames = append(userNames, s.Config.UserName())
	} else {
		for i := 0; i < fs.NArg(); i++ {
			userName, err := user.Clean(upspin.UserName(fs.Arg(i)))
			if err != nil {
				s.Exit(err)
			}
			userNames = append(userNames, userName)
		}
	}
	for _, name := range userNames {
		u, err := keyServer.Lookup(name)
		if err != nil {
			s.Exit(err)
		}
		blob, err := yaml.Marshal(u)
		if err != nil {
			// TODO(adg): better error message?
			s.Exit(err)
		}
		fmt.Printf("%s\n", blob)
		if name != s.Config.UserName() {
			continue
		}
		// When it's the user asking about herself, the result comes
		// from the configuration and may disagree with the value in the
		// key store. This is a common source of error so we want to
		// diagnose it. To do that, we wipe the key cache and go again.
		// This will wipe the memory of our remembered configuration and
		// reload it from the key server.
		usercache.ResetGlobal()
		keyU, err := keyServer.Lookup(name)
		if err != nil {
			s.Exit(err)
		}
		var buf bytes.Buffer
		if keyU.Name != u.Name {
			fmt.Fprintf(&buf, "user name in configuration: %s\n", u.Name)
			fmt.Fprintf(&buf, "user name in key server: %s\n", keyU.Name)
		}
		if keyU.PublicKey != u.PublicKey {
			fmt.Fprintf(&buf, "public key in configuration does not match key server\n")
		}
		// There must be dir servers defined in both and we expect agreement.
		if !equalEndpoints(keyU.Dirs, u.Dirs) {
			fmt.Fprintf(&buf, "dirs in configuration: %s\n", u.Dirs)
			fmt.Fprintf(&buf, "dirs in key server: %s\n", keyU.Dirs)
		}
		// Remote stores need not be defined (yet).
		if len(keyU.Stores) > 0 && !equalEndpoints(keyU.Stores, u.Stores) {
			fmt.Fprintf(&buf, "stores in configuration: %s\n", u.Stores)
			fmt.Fprintf(&buf, "stores in key server: %s\n", keyU.Stores)
		}
		if buf.Len() > 0 {
			s.Exitf("local configuration differs from public record in key server:\n%s", &buf)
		}
	}
}

func equalEndpoints(a, b []upspin.Endpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i, e := range a {
		if e != b[i] {
			return false
		}
	}
	return true
}

func (s *State) putUser(fs *flag.FlagSet, keyServer upspin.KeyServer, inFile string, force bool) {
	data := s.ReadAll(inFile)
	userStruct := new(upspin.User)
	err := yaml.Unmarshal(data, userStruct)
	if err != nil {
		// TODO(adg): better error message?
		s.Exit(err)
	}
	if fs.NArg() != 0 && upspin.UserName(fs.Arg(0)) != userStruct.Name {
		s.Exitf("User name provided does not match the one read from the input file.")
	}

	// Validate public key.
	if userStruct.PublicKey == "" && !force {
		s.Exitf("An empty public key will prevent user from accessing services. To override use -force.")
	}
	_, err = factotum.ParsePublicKey(userStruct.PublicKey)
	if err != nil && !force {
		s.Exitf("invalid public key, to override use -force: %s", err.Error())
	}
	// Clean the username.
	userStruct.Name, err = user.Clean(userStruct.Name)
	if err != nil {
		s.Exit(err)
	}
	err = keyServer.Put(userStruct)
	if err != nil {
		s.Exit(err)
	}
}
