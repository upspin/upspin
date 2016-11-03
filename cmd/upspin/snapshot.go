// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"

	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) snapshot(args ...string) {
	const help = `
Snapshot requests the servers to take a snapshot of the tree as soon as
possible. Snapshots are only created if there is a tree rooted at '%s'.
`
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)

	u, suffix, domain, err := user.Parse(s.context.UserName())
	if err != nil {
		s.exit(err)
	}
	var suffixedUser string
	if suffix == "" {
		suffixedUser = u + "+snapshot@" + domain
	} else {
		suffixedUser = u[:len(u)-len(suffix)-1] + "+snapshot@" + domain
	}

	s.parseFlags(fs, args, fmt.Sprintf(help, suffixedUser), "snapshot")
	if fs.NArg() > 0 {
		fs.Usage()
	}

	// Put a new DirEntry that triggers the snapshotting process.
	name := path.Join(upspin.PathName(suffixedUser), "TakeSnapshot")
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Packing:    upspin.PlainPack,
	}
	dir, err := s.client.DirServer(entry.Name)
	if err != nil {
		s.exit(err)
	}
	_, err = dir.Put(entry)
	if err != nil {
		s.exit(err)
	}
}
