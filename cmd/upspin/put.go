// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/path"
	"upspin.io/upspin"
)

func (s *State) put(args ...string) {
	const help = `
Put writes its input to the store server and installs a directory
entry with the given path name to refer to the data.

TODO: Delete in favor of cp?
`
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	s.parseFlags(fs, args, help, "put [-in=inputfile] path")

	if fs.NArg() != 1 {
		fs.Usage()
	}

	data := s.readAll(*inFile)
	// Must be a valid Upspin name.
	parsed, err := path.Parse(upspin.PathName(fs.Arg(0)))
	if err != nil {
		s.exit(err)
	}
	name := parsed.Path()
	if hasGlobChar(parsed.String()) {
		// If there is a metacharacter in the last element, the whole path
		// must exist. Otherwise, only the path up to the last element (its
		// directory) must exist. We call Glob appropriately.
		if hasGlobChar(parsed.Elem(parsed.NElem() - 1)) {
			dir := s.globOneUpspinPath(parsed.Drop(1).String())
			name = path.Join(dir, parsed.Elem(parsed.NElem()-1))
		} else {
			name = s.globOneUpspinPath(parsed.String())
		}
	}
	_, err = s.client.Put(name, data)
	if err != nil {
		s.exit(err)
	}
}
