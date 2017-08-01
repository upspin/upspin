// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/bind"
	"upspin.io/upspin"
)

func (s *State) deletestorage(args ...string) {
	const help = `
Deletestorage deletes blocks from the store. It is given
either a list of path names, in which case it deletes all blocks
referenced by those names, or a list of references, in which case
it deletes the blocks with those references.

WARNING! Deletestorage is dangerous and should not be used unless
the user can guarantee that the blocks that will be deleted are not
referenced by another path name in any other directory tree, including
snapshots.

Exactly one of the -path or -ref flags must be specified.

For -path, only regular items (not links or directories) can be
processed. Each block will be removed from the store on which it
resides, which in exceptional circumstances may be different from
the user's store.

For -ref, the reference must exactly match the reference's full
value, such as is presented by the info command. The reference is
assumed to refer to the store defined in the user's configuration.
`
	fs := flag.NewFlagSet("deletestorage", flag.ExitOnError)
	byPath := fs.Bool("path", false, "delete all blocks referenced by the path names")
	byRef := fs.Bool("ref", false, "delete individual blocks with the specified references")
	s.ParseFlags(fs, args, help, "deletestorage [-path path... | -ref reference...]")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	if *byRef == *byPath { // Exactly one must be set.
		usageAndExit(fs)
	}

	if *byRef {
		// All references refer to this store.
		store, err := bind.StoreServer(s.Config, s.Config.StoreEndpoint())
		if err != nil {
			s.Exit(err)
		}
		for _, arg := range fs.Args() {
			err := store.Delete(upspin.Reference(arg))
			if err != nil {
				// Keep going, for consistency with loop below.
				s.Fail(err)
			}
		}
		return
	}

	var prevEndpoint upspin.Endpoint
	var store upspin.StoreServer
	for _, entry := range s.GlobAllUpspin(fs.Args()) {
		if !entry.IsRegular() {
			s.Exitf("%s is not a plain file", entry.Name)
		}
		for _, block := range entry.Blocks {
			if block.Location.Endpoint != prevEndpoint {
				prevEndpoint = block.Location.Endpoint
				var err error
				store, err = bind.StoreServer(s.Config, prevEndpoint)
				if err != nil {
					s.Exit(err) // Not much to do now.
				}
			}
			err := store.Delete(block.Location.Reference)
			if err != nil {
				// Here we keep going, to keep it possible to delete
				// other existing references.
				s.Fail(err)
			}
		}
	}
}
