// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/upspin"
)

func (s *State) link(args ...string) {
	const help = `
Link creates an Upspin link. The link is created at the second path
argument and points to the first path argument.
`
	var force bool
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	fs.BoolVar(&force, "f", false, "force creation of link when original path is inaccessible")
	// This is the same order as in the Unix ln command. It feels sort of
	// backwards, but it's also the same as in cp, with the new name second.
	s.ParseFlags(fs, args, help, "link [-f] original_path link_path")
	if fs.NArg() != 2 {
		usageAndExit(fs)
	}

	originalPath := upspin.PathName(s.AtSign(fs.Arg(0)))
	linkPath := upspin.PathName(s.AtSign(fs.Arg(1)))

	if !force {
		_, err := s.Client.Lookup(originalPath, false)
		if err != nil {
			s.Exit(err)
		}
	}

	_, err := s.Client.PutLink(originalPath, linkPath)
	if err != nil {
		s.Exit(err)
	}
}
