// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/upspin"
)

func (s *State) rm(args ...string) {
	const help = `
Rm removes Upspin files and directories from the name space.

The -glob flag can be set to false to have rm skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

Rm does not delete the associated storage, which is rarely necessary
or wise: storage can be shared between items and unused storage is
better recovered by automatic means.

Rm does not delete the targets of links, only the links themselves.

See the deletestorage command for more information about deleting
storage.
`
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	recur := fs.Bool("R", false, "recur into subdirectories")
	continueOnError := fs.Bool("f", false, "continue if errors occur")
	glob := globFlag(fs)
	s.ParseFlags(fs, args, help, "rm path...")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	exit := s.Exit
	if *continueOnError {
		exit = s.Fail
	}
	for _, name := range s.expandUpspin(fs.Args(), *glob) {
		entry, err := s.Client.Lookup(name, false)
		if err != nil {
			exit(err)
			continue
		}
		s.remove(entry, *recur, exit)
	}
}

// remove deletes the entry. If recur is set and entry is a directory, it first
// removes the contents of the directory.
func (s *State) remove(entry *upspin.DirEntry, recur bool, exit func(error)) {
	if recur && entry.IsDir() {
		// Delete the contents of the directory first. Dir is not a link so
		// Client.Glob is fine.
		dirContents, err := s.Client.Glob(upspin.AllFilesGlob(entry.Name))
		if err != nil {
			exit(err)
			return
		}
		for _, e := range dirContents {
			s.remove(e, recur, exit)
		}
		// Now fall through to delete directory.
	}
	err := s.Client.Delete(entry.Name)
	if err != nil {
		exit(err)
		return
	}
}
