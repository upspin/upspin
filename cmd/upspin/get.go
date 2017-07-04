// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
)

func (s *State) get(args ...string) {
	const help = `
Get writes to standard output the contents identified by the Upspin path.
`
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	s.ParseFlags(fs, args, help, "get [-out=outputfile] path")

	names := s.GlobAllUpspinPath(fs.Args())
	if len(names) != 1 {
		usageAndExit(fs)
	}

	data, err := s.Client.Get(names[0])
	if err != nil {
		s.Exit(err)
	}
	s.writeOut(*outFile, data)
}
