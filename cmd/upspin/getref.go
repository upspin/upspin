// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"

	"upspin.io/bind"
	"upspin.io/subcmd"
	"upspin.io/upspin"
)

func (s *State) getref(args ...string) {
	const help = `
Getref writes to standard output the contents identified by the reference from
the user's default store server. It does not resolve redirections.
`
	fs := flag.NewFlagSet("getref", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	s.ParseFlags(fs, args, help, "getref [-out=outputfile] ref")

	if fs.NArg() != 1 {
		fs.Usage()
	}
	ref := fs.Arg(0)

	store, err := bind.StoreServer(s.Config, s.Config.StoreEndpoint())
	if err != nil {
		s.Exit(err)
	}
	fmt.Fprintf(os.Stderr, "Using store server at %s\n", s.Config.StoreEndpoint())

	data, _, locs, err := store.Get(upspin.Reference(ref))
	if err != nil {
		s.Exit(err)
	}
	if len(locs) > 0 {
		fmt.Fprintf(os.Stderr, "Redirection detected:\n")
		for _, loc := range locs {
			fmt.Fprintf(os.Stderr, "%+v\n", loc)
		}
		return
	}

	// Write to outfile or to stdout if none set.
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
