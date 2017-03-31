// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
)

func (s *State) watch(args ...string) {
	const help = `
Watch watches the given Upspin path beginning with the specified order and
prints the events to standard output. An order of -1, the default, will send
the current state of the tree rooted at the given path.
`
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	order := fs.Int64("order", -1, "order")
	s.ParseFlags(fs, args, help, "watch [-order=n] path")

	names := s.GlobAllUpspinPath(fs.Args())
	if len(names) != 1 {
		fs.Usage()
	}
	name := names[0]

	dir, err := s.Client.DirServer(name)
	if err != nil {
		s.Exit(err)
	}

	done := make(chan struct{})
	events, err := dir.Watch(name, *order, done)
	if err != nil {
		s.Exit(err)
	}
	for e := range events {
		if e.Error != nil {
			fmt.Fprintf(os.Stderr, "watch: error: %s\n", e.Error) // TODO: Failf? Set exitCode?
			continue
		}

		de := e.Entry
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
		fmt.Printf("%s [%s] %s %s\n", de.Time, attr, size, de.Name)
	}
}
