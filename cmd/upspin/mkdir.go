// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) mkdir(args ...string) {
	const help = `
Mkdir creates Upspin directories.

The -glob flag can be set to false to have mkdir skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	glob := globFlag(fs)
	s.ParseFlags(fs, args, help, "mkdir directory...")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	for _, name := range s.expandUpspin(fs.Args(), *glob) {
		_, err := s.Client.MakeDirectory(name)
		if err != nil {
			s.Exit(err)
		}
	}
}
