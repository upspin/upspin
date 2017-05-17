// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) mkdir(args ...string) {
	const help = `
Mkdir creates Upspin directories.
`
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "mkdir directory...")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	for _, name := range s.GlobAllUpspinPath(fs.Args()) {
		_, err := s.Client.MakeDirectory(name)
		if err != nil {
			s.Exit(err)
		}
	}
}
