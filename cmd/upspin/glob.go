// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
)

func (s *State) glob(args ...string) {
	const help = `
Glob prints the names of the paths that are matched by its arguments.
It does no further interpretation of the returned path names.
`
	fs := flag.NewFlagSet("glob", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "glob pattern...")

	for _, entry := range s.GlobAllUpspin(fs.Args()) {
		s.Printf("%s\n", entry.Name)
	}
}
