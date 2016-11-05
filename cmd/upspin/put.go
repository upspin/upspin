// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) put(args ...string) {
	const help = `
Put writes its input to the store server and installs a directory
entry with the given path name to refer to the data.

TODO: Delete in favor of cp?
`
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	s.parseFlags(fs, args, help, "put [-in=inputfile] path")

	names := s.globAllUpspin(fs.Args())
	if len(names) != 1 {
		fs.Usage()
	}

	data := s.readAll(*inFile)
	_, err := s.client.Put(names[0], data)
	if err != nil {
		s.exit(err)
	}
}
