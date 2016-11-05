// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) link(args ...string) {
	const help = `
Link creates an Upspin link. The link is created at the first path
argument and points to the second path argument.
`
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	// This is the same order as in the Unix ln command. It sorta feels
	// backwards, but it's also the same as in cp, with the new name second.
	s.parseFlags(fs, args, help, "link original_path link_path")
	files := s.globAllUpspin(fs.Args())
	if len(files) != 2 {
		fs.Usage()
	}

	originalPath := files[0]
	linkPath := files[1]
	if len(originalPath) != 1 || len(linkPath) != 1 {
		fs.Usage()
	}

	_, err := s.client.PutLink(originalPath, linkPath)
	if err != nil {
		s.exit(err)
	}
}
