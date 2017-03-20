// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func (s *State) whichAccess(args ...string) {
	const help = `
Whichaccess reports the Upspin path of the Access file
that controls permissions for each of the argument paths.
`
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "whichaccess path...")
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.GlobAllUpspinPath(fs.Args()) {
		acc, err := s.whichAccessFollowLinks(name)
		if err != nil {
			s.Exit(err)
		}
		if acc == nil {
			fmt.Printf("%s: owner only\n", name)
		} else {
			fmt.Printf("%s: %s\n", name, acc.Name)
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
		if prevEntry != nil && errors.Match(errors.E(errors.NotExist), err) {
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
