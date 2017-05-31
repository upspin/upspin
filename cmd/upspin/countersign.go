// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main // import "upspin.io/cmd/upspin"

// This file has the implementation of the countersign command.  Invoke before publishing the new keys.

import (
	"flag"

	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/log"
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
		usageAndExit(fs)
	}
	s.countersignCommand(fs)
}

// Countersigner holds the new and old states for the countersign calculation.
type Countersigner struct {
	state    *State // state.Config.Factotum() holds new key as primary, old keys in archive
	oldState *State // oldState.Config.Factotum() holds the old as primary, new in archive
}

// countersignCommand is the main function for the countersign subcommand.
func (s *State) countersignCommand(fs *flag.FlagSet) {
	// o = copy(s) with adjusted factotum, analogous to init() in main.go
	o := newState(s.Name)
	o.State.Init(config.SetFactotum(s.Config, s.Config.Factotum().Pop()))
	o.sharer = newSharer(o)
	o.enableMetrics()

	pub := s.Config.Factotum().PublicKey()
	log.Debug.Printf("countersign new\n  hash %x\n  key %s", factotum.KeyHash(pub), pub)
	pub = o.Config.Factotum().PublicKey()
	log.Debug.Printf("countersign old\n  hash %x\n  key %s", factotum.KeyHash(pub), pub)
	c := &Countersigner{
		state:    s,
		oldState: o,
	}
	root := upspin.PathName(string(s.Config.UserName()) + "/")
	entries := c.entriesFromDirectory(root)
	for _, e := range entries {
		c.countersign(e)
	}
}

// countersign adds a second signature using factotum.
func (c *Countersigner) countersign(entry *upspin.DirEntry) {
	packer := lookupPacker(entry)
	newF := c.state.Config.Factotum()
	oldKey := c.oldState.Config.Factotum().PublicKey()
	err := packer.Countersign(oldKey, newF, entry)
	if err != nil {
		c.state.Fail(err)
		return
	}
	_, err = c.oldState.DirServer(entry.Name).Put(entry)
	if err != nil {
		// If we get ErrFollowLink, the item changed underfoot, so reporting
		// an error in that case is OK.
		c.state.Failf("error putting entry back for %q: %s\n", entry.Name, err)
	}
}

// entriesFromDirectory returns the list of relevant entries in the directory, recursively.
func (c *Countersigner) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	thisDir, err := c.oldState.DirServer(dir).Glob(upspin.AllFilesGlob(dir)) // Do not want to follow links.
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
