// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main // import "upspin.io/cmd/upspin"

// This file has the implementation of the countersign command.  Invoke before publishing the new keys.

import (
	"flag"

	"upspin.io/config"
	"upspin.io/upspin"
)

func (s *State) countersign(args ...string) {
	const help = `
Countersign updates the signatures and encrypted data for all items
owned by the user. It is intended to be run after a user has changed
keys.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("countersign", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "countersign")
	if fs.NArg() != 0 {
		fs.Usage()
	}
	s.countersignCommand(fs)
}

// Countersigner holds the state for the countersign calculation.
type Countersigner struct {
	state  *State
	oldKey upspin.PublicKey
}

// countersignCommand is the main function for the countersign subcommand.
func (s *State) countersignCommand(fs *flag.FlagSet) {
	u, err := s.KeyServer().Lookup(s.Config.UserName())
	if err != nil || len(u.PublicKey) == 0 {
		s.Exitf("can't find old key for %q: %s\n", s.Config.UserName(), err)
	}
	c := &Countersigner{
		state:  s,
		oldKey: u.PublicKey,
	}
	newF := s.Config.Factotum()
	if newF == nil {
		s.Exitf("no factotum available")
	}

	lastCfg := s.Config
	s.Config = config.SetFactotum(s.Config, s.Config.Factotum().Pop())
	defer func() { s.Config = lastCfg }()

	root := upspin.PathName(string(s.Config.UserName()) + "/")
	entries := c.entriesFromDirectory(root)
	for _, e := range entries {
		c.countersign(e, newF)
	}
}

// countersign adds a second signature using factotum.
func (c *Countersigner) countersign(entry *upspin.DirEntry, newF upspin.Factotum) {
	packer := lookupPacker(entry)
	err := packer.Countersign(c.oldKey, newF, entry)
	if err != nil {
		c.state.Fail(err)
		return
	}
	_, err = c.state.DirServer(entry.Name).Put(entry)
	if err != nil {
		// If we get ErrFollowLink, the item changed underfoot, so reporting
		// an error in that case is OK.
		c.state.Failf("error putting entry back for %q: %s\n", entry.Name, err)
	}
}

// entriesFromDirectory returns the list of relevant entries in the directory, recursively.
func (c *Countersigner) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	thisDir, err := c.state.DirServer(dir).Glob(upspin.AllFilesGlob(dir)) // Do not want to follow links.
	if err != nil {
		c.state.Exitf("globbing %q: %s", dir, err)
	}
	entries := make([]*upspin.DirEntry, 0, len(thisDir))
	// Add plain files that have signatures by self.
	for _, e := range thisDir {
		if e.IsDir() {
			continue
		}
		if e.Writer == c.state.Config.UserName() {
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
