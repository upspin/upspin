// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"

	"upspin.io/path"
	"upspin.io/upspin"
)

func (s *State) glob(args ...string) {
	const help = `
Glob prints the names of the paths that are matched by its arguments.
It does no further interpretation of the returned path names.
`
	fs := flag.NewFlagSet("glob", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "glob pattern...")

	fmt.Println("FROM CLIENT")
	for _, entry := range s.GlobAllUpspin(fs.Args()) {
		s.Printf("%s\n", entry.Name)
	}
	fmt.Println("FROM SERVER")
	for _, arg := range fs.Args() {
		pat := s.AtSign(arg)
		parsed, err := path.Parse(upspin.PathName(pat))
		if err != nil {
			s.Exit(err)
		}
		entries, err := s.DirServer(upspin.PathName(parsed.User())).Glob(string(pat))
		if err != nil {
			s.Exit(err)
		}
		for _, entry := range entries {
			s.Printf("%s\n", entry.Name)
		}
	}
}
