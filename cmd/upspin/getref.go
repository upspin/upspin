// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"

	"upspin.io/bind"
	"upspin.io/upspin"
)

func (s *State) getref(args ...string) {
	const help = `
Getref writes to standard output the contents identified by the reference from
the specified store endpoint, by default the user's default store server.
It does not resolve redirections.
`
	fs := flag.NewFlagSet("getref", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	store := fs.String("store", "", "store endpoint (default the user's store)")
	s.ParseFlags(fs, args, help, "getref [-store endpoint] [-out=outputfile] ref")

	if fs.NArg() != 1 {
		usageAndExit(fs)
	}
	ref := fs.Arg(0)

	endpoint := s.Config.StoreEndpoint()
	if *store != "" {
		e, err := upspin.ParseEndpoint(*store)
		if err != nil {
			s.Exit(err)
		}
		endpoint = *e
	}

	storeServer, err := bind.StoreServer(s.Config, endpoint)
	if err != nil {
		s.Exit(err)
	}

	data, _, locs, err := storeServer.Get(upspin.Reference(ref))
	if err != nil {
		s.Exit(err)
	}
	if len(locs) > 0 {
		fmt.Fprintln(s.Stderr, "Redirection detected:")
		for _, loc := range locs {
			fmt.Fprintf(s.Stderr, "%+v\n", loc)
		}
		return
	}

	s.writeOut(*outFile, data)
}
