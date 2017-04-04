// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"os"

	"upspin.io/subcmd"
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
		fs.Usage()
	}

	data, err := s.Client.Get(names[0])
	if err != nil {
		s.Exit(err)
	}
	// Write to outfile or to stdout if none set
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		output = s.CreateLocal(subcmd.Tilde(*outFile))
		defer output.Close()
	}
	_, err = output.Write(data)
	if err != nil {
		s.Exitf("Copying to output failed: %v", err)
	}
}
