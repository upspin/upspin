// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) snapshot(args ...string) {
	const help = `
Snapshot requests the system to take a snapshot of the user's
directory tree as soon as possible. Snapshots are created only if
the directory server for the user's root supports them.
`
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "snapshot")
	if fs.NArg() > 0 {
		fs.Usage()
	}

	u, suffix, domain, err := user.Parse(s.Config.UserName())
	if err != nil {
		s.Exit(err)
	}
	var suffixedUser string
	if suffix == "" {
		suffixedUser = u + "+snapshot@" + domain
	} else {
		suffixedUser = u[:len(u)-len(suffix)-1] + "+snapshot@" + domain
	}

	// Is the root for the snapshot already created?
	_, err = s.Client.Lookup(upspin.PathName(suffixedUser), false)
	if err != nil && errors.Match(errors.E(errors.NotExist), err) {
		_, err = s.Client.MakeDirectory(upspin.PathName(suffixedUser + "/"))
		if err != nil {
			s.Exit(err)
		}
	} else if err != nil {
		s.Exit(err)
	}

	// Put a new DirEntry that triggers the snapshotting process.
	// Note: This is a hack, but it works. See dir/server/snapshot.go for
	// the mechanism.
	// TODO: Find a cleaner mechanism?
	name := path.Join(upspin.PathName(suffixedUser), "TakeSnapshot")
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Packing:    upspin.PlainPack,
	}
	s.DirServer(entry.Name).Put(entry)
}
