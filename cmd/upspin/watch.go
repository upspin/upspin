// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
)

func (s *State) watch(args ...string) {
	const help = `
Watch watches the given Upspin path beginning with the specified
sequence number and prints the events to standard output. A sequence
number of -1, the default, will send the current state of the tree
rooted at the given path.

The -glob flag can be set to false to have watch skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	glob := globFlag(fs)
	sequence := fs.Int64("sequence", -1, "`sequence` number")
	s.ParseFlags(fs, args, help, "watch [-sequence=n] path")

	names := s.expandUpspin(fs.Args(), *glob)
	if len(names) != 1 {
		usageAndExit(fs)
	}
	name := names[0]

	dir, err := s.Client.DirServer(name)
	if err != nil {
		s.Exit(err)
	}

	done := make(chan struct{})
	events, err := dir.Watch(name, *sequence, done)
	if err != nil {
		s.Exit(err)
	}
	for e := range events {
		if e.Error != nil {
			fmt.Fprintf(s.Stderr, "watch: error: %s\n", e.Error) // TODO: Failf? Set exitCode?
			continue
		}

		de := e.Entry
		seq := fmt.Sprintf("%10d", de.Sequence)
		attr := []byte("file")
		if de.IsDir() {
			copy(attr, "dir ")
		} else if de.IsLink() {
			copy(attr, "link")
		}
		if de.IsIncomplete() {
			attr[3] = '!'
		}
		size := "          "
		if e.Delete {
			size = " [deleted]"
		} else if de.IsRegular() && !de.IsIncomplete() {
			d, _ := de.Size()
			size = fmt.Sprintf("%10d", d)
		}
		s.Printf("%s %s [%s] %s %s\n", de.Time, seq, attr, size, de.Name)
	}
}
