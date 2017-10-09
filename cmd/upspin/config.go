// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) config(args ...string) {
	const help = `
Config prints to standard output the contents of the current config file.

It works by saving the file at initialization time, so if the actual
file has changed since the command started, it will still show the
configuration being used.
`
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	s.ParseFlags(fs, args, help, "config [-out=outputfile]")

	s.writeOut(*outFile, s.configFile)
}
