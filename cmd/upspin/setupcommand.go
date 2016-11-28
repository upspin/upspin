// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const help = `
Setupdomain sets up all configuration files for a new domain or overwrites them.
`

	fs := flag.NewFlagSet("setupdomain", flag.ExitOnError)
	//	where := fs.String("where", "", "`directory` to store keys; default $HOME/upspin/deploy")
	s.parseFlags(fs, args, help, "setupdomain -owner=<upspin_username> [-where=$HOME/upspin/deploy]")
	if fs.NArg() != 0 {
		fs.Usage()
	}

	//
}
