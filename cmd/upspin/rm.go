// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) rm(args ...string) {
	const help = `
Rm removes Upspin files and directories from the name space.

Rm does not delete the associated storage, which is rarely necessary
or wise: storage can be shared between items and unused storage is
better recovered by automatic means.

See the deletestorage command for more information about deleting
storage.
`
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "rm path...")
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.GlobAllUpspinPath(fs.Args()) {
		err := s.Client.Delete(name)
		if err != nil {
			s.Exit(err)
		}
	}
}
