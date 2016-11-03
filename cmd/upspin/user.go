// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"upspin.io/factotum"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) user(args ...string) {
	const help = `
User prints in JSON format the user record stored in the key server
for the specified user, by default the current user.

With the -put flag, user writes or replaces the information stored
for the current user. It can be used to update keys for the user;
for new users see the signup command. The information is read
from standard input or from the file provided with the -in flag.
It must be the complete record for the user, and must be in the
same JSON format printed by the command without the -put flag.
`
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	put := fs.Bool("put", false, "write new user record")
	inFile := fs.String("in", "", "input file (default standard input)")
	force := fs.Bool("force", false, "force writing user record even if key is empty")
	// TODO: the username is not accepted with -put. We may need two lines to fix this (like 'man printf').
	s.parseFlags(fs, args, help, "user [-put [-in=inputfile] [-force]] [username...]")
	keyServer := s.KeyServer()
	if *put {
		if fs.NArg() != 0 {
			fs.Usage()
		}
		s.putUser(keyServer, *inFile, *force)
		return
	}
	if *inFile != "" {
		s.exitf("-in only available with -put")
	}
	if *force {
		s.exitf("-force only available with -put")
	}
	var userNames []upspin.UserName
	if fs.NArg() == 0 {
		userNames = append(userNames, s.context.UserName())
	} else {
		for i := 0; i < fs.NArg(); i++ {
			userName, err := user.Clean(upspin.UserName(fs.Arg(i)))
			if err != nil {
				s.exit(err)
			}
			userNames = append(userNames, userName)
		}
	}
	for _, name := range userNames {
		u, err := keyServer.Lookup(name)
		if err != nil {
			s.exit(err)
		}
		blob, err := json.MarshalIndent(u, "", "\t")
		if err != nil {
			// TODO(adg): better error message?
			s.exit(err)
		}
		fmt.Printf("%s\n", blob)
	}
}

func (s *State) putUser(keyServer upspin.KeyServer, inFile string, force bool) {
	data := s.readAll(inFile)
	userStruct := new(upspin.User)
	err := json.Unmarshal(data, userStruct)
	if err != nil {
		// TODO(adg): better error message?
		s.exit(err)
	}
	// Validate public key.
	if userStruct.PublicKey == "" && !force {
		s.exitf("An empty public key will prevent user from accessing services. To override use -force.")
	}
	_, _, err = factotum.ParsePublicKey(userStruct.PublicKey)
	if err != nil && !force {
		s.exitf("invalid public key, to override use -force: %s", err.Error())
	}
	// Clean the username.
	userStruct.Name, err = user.Clean(userStruct.Name)
	if err != nil {
		s.exit(err)
	}
	err = keyServer.Put(userStruct)
	if err != nil {
		s.exit(err)
	}
}
