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
assumed to refer to the store defined in the user's context.
`
	fs := flag.NewFlagSet("deletestorage", flag.ExitOnError)
	byPath := fs.Bool("path", false, "delete all blocks referenced by the path names")
	byRef := fs.Bool("ref", false, "delete individual blocks with the specified references")
	s.parseFlags(fs, args, help, "deletestorage [-path path... | -ref reference...]")
	if fs.NArg() == 0 {
		fs.Usage()
	}
	if *byRef == *byPath { // Exactly one must be set.
		fs.Usage()
	}

	if *byRef {
		// All references refer to this store.
		store, err := bind.StoreServer(s.context, s.context.StoreEndpoint())
		if err != nil {
			s.exit(err)
		}
		for _, arg := range fs.Args() {
			err := store.Delete(upspin.Reference(arg))
			if err != nil {
				s.exit(err)
			}
		}
		return
	}

	var prevEndpoint upspin.Endpoint
	var store upspin.StoreServer
	for _, arg := range fs.Args() {
		entry, err := s.DirServer().Lookup(upspin.PathName(arg))
		if err != nil {
			s.exit(err)
		}
		if entry.Attr != upspin.AttrNone {
			s.exitf("%s is not a plain file", arg)
		}
		for _, block := range entry.Blocks {
			if block.Location.Endpoint != prevEndpoint {
				prevEndpoint = block.Location.Endpoint
				store, err = bind.StoreServer(s.context, prevEndpoint)
				if err != nil {
					s.exit(err)
				}
			}
			err := store.Delete(block.Location.Reference)
			if err != nil {
				s.exit(err)
			}
		}
	}
	return
}
