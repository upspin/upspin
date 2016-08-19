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

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
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
// TODO(ehg) Learn why Countersigner is capitalized and receiver is a pointer.
type Countersigner struct {
	exitCode int // Exit with non-zero status for minor problems.
	context  upspin.Context
	client   upspin.Client
	oldKey   upspin.PublicKey
}

var countersigner Countersigner

func (s *Countersigner) init() {
	context, err := context.InitContext(nil)
	if err != nil {
		exitf("initializing context: %s", err)
	}
	s.context = context
	s.client = client.New(context)
	s.oldKey = s.lookupKey(s.context.UserName()) // the one at keyserver
}

// countersignCommand is the main function for the countersign subcommand.
func (s *Countersigner) countersignCommand() {
	newF := s.context.Factotum()
	oldF := newF.Pop()
	s.context.SetFactotum(oldF) // so calls to servers Authenticate using old key
	defer s.context.SetFactotum(newF)
	root := upspin.PathName(string(s.context.UserName()) + "/")
	entries := s.entriesFromDirectory(root)
	for _, e := range entries {
		s.countersign(e, newF)
	}
	os.Exit(s.exitCode)
}

// countersign adds a second signature using factotum.
func (s *Countersigner) countersign(entry *upspin.DirEntry, newF upspin.Factotum) {
	err := ee.Countersign(s.oldKey, newF, entry)
	if err != nil {
		exit(err)
	}
	directory, err := bind.DirServer(s.context, s.context.DirEndpoint())
	if err != nil {
		exit(err)
	}
	_, err = directory.Put(entry)
	if err != nil {
		// TODO: implement links.
		fmt.Fprintf(os.Stderr, "error putting entry back for %q: %s\n", entry.Name, err)
		s.exitCode = 1
	}
}

// entriesFromDirectory returns the list of relevant entries in the directory, recursively.
func (s *Countersigner) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	directory, err := bind.DirServer(s.context, s.context.DirEndpoint())
	if err != nil {
		exit(err)
	}
	thisDir, err := directory.Glob(string(dir) + "/*")
	if err != nil {
		exitf("globbing %q: %s", dir, err)
	}
	entries := make([]*upspin.DirEntry, 0, len(thisDir))
	// Add plain files that have signatures by self.
	for _, e := range thisDir {
		if !e.IsDir() && !e.IsLink() &&
			e.Packing == upspin.EEPack &&
			string(e.Writer) == string(s.context.UserName()) {
			entries = append(entries, e)
		}
	}
	// Recur into subdirectories.
	for _, e := range thisDir {
		if e.IsDir() {
			entries = append(entries, s.entriesFromDirectory(e.Name)...)
		}
	}
	return entries
}

// lookupKey returns the public key for the user.
func (s *Countersigner) lookupKey(user upspin.UserName) upspin.PublicKey {
	userService, err := bind.KeyServer(s.context, s.context.KeyEndpoint())
	if err != nil {
		exit(err)
	}
	u, err := userService.Lookup(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't find key for %q: %s\n", user, err)
		s.exitCode = 1
		return ""
	}
	key := u.PublicKey
	if len(key) == 0 {
		fmt.Fprintf(os.Stderr, "no key for %q\n", user)
		s.exitCode = 1
	}
	return key
}
