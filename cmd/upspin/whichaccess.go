// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func (s *State) whichAccess(args ...string) {
	const help = `
Whichaccess reports the Upspin path of the Access file
that controls permissions for each of the argument paths.

The -glob flag can be set to false to have watchaccess skip Glob
processing, treating its arguments as literal text even if they
contain special characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	glob := globFlag(fs)
	s.ParseFlags(fs, args, help, "whichaccess path...")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	for _, name := range s.expandUpspin(fs.Args(), *glob) {
		acc, err := s.whichAccessFollowLinks(name)
		if err != nil {
			s.Exit(err)
		}
		if acc == nil {
			s.Printf("%s: owner only\n", name)
		} else {
			s.Printf("%s: %s\n", name, acc.Name)
		}
	}
}

func (s *State) whichAccessFollowLinks(name upspin.PathName) (*upspin.DirEntry, error) {
	var prevEntry *upspin.DirEntry
	for loop := 0; loop < upspin.MaxLinkHops; loop++ {
		entry, err := s.DirServer(name).WhichAccess(name)
		if err == upspin.ErrFollowLink {
			name = entry.Link
			continue
		}
		if prevEntry != nil && errors.Is(errors.NotExist, err) {
			return nil, errors.E(errors.BrokenLink, prevEntry.Name, err)
		}
		prevEntry = entry
		if err != nil {
			return nil, err
		}
		return entry, nil
	}
	s.Exitf("%s: link loop", name)
	return nil, nil
}
