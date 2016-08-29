// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Countersign has utility functions for updating signatures of encrypted items
// after users update their keys.  Invoke before publishing the new keys.

// Derived from ./share.go.

import (
	"fmt"
	"os"

	"upspin.io/pack/ee"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"
)

// Countersigner holds the state for the countersign calculation.
type Countersigner struct {
	state  *State
	oldKey upspin.PublicKey
}

func newCountersigner(s *State) *Countersigner {
	c := &Countersigner{
		state: s,
	}
	u, err := s.KeyServer().Lookup(s.context.UserName())
	if err != nil || len(u.PublicKey) == 0 {
		s.exitf("can't find old key for %q: %s\n", s.context.UserName(), err)
	}
	c.oldKey = u.PublicKey
	return c
}

// countersignCommand is the main function for the countersign subcommand.
func (s *State) countersignCommand() {
	newF := s.context.Factotum()
	oldF := newF.Pop()
	s.context.SetFactotum(oldF) // so calls to servers Authenticate using old key
	defer s.context.SetFactotum(newF)
	root := upspin.PathName(string(s.context.UserName()) + "/")
	c := s.countersigner
	entries := c.entriesFromDirectory(root)
	for _, e := range entries {
		c.countersign(e, newF)
	}
}

// countersign adds a second signature using factotum.
func (c *Countersigner) countersign(entry *upspin.DirEntry, newF upspin.Factotum) {
	err := ee.Countersign(c.oldKey, newF, entry)
	if err != nil {
		c.state.exit(err)
	}
	_, err = c.state.DirServer().Put(entry)
	if err != nil {
		// TODO: implement links.
		fmt.Fprintf(os.Stderr, "error putting entry back for %q: %s\n", entry.Name, err)
		c.state.exitCode = 1
	}
}

// entriesFromDirectory returns the list of relevant entries in the directory, recursively.
func (c *Countersigner) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	thisDir, err := c.state.DirServer().Glob(string(dir) + "/*") // Do not want to follow links.
	if err != nil {
		c.state.exitf("globbing %q: %s", dir, err)
	}
	entries := make([]*upspin.DirEntry, 0, len(thisDir))
	// Add plain files that have signatures by self.
	for _, e := range thisDir {
		if !e.IsDir() && !e.IsLink() &&
			e.Packing == upspin.EEPack &&
			string(e.Writer) == string(c.state.context.UserName()) {
			entries = append(entries, e)
		}
	}
	// Recur into subdirectories.
	for _, e := range thisDir {
		if e.IsDir() {
			entries = append(entries, c.entriesFromDirectory(e.Name)...)
		}
	}
	return entries
}
