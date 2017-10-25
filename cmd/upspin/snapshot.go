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
		usageAndExit(fs)
	}

	u, suffix, domain, err := user.Parse(s.Config.UserName())
	if err != nil {
		s.Exit(err)
	}
	var snapshotUser upspin.UserName
	if suffix == "" {
		snapshotUser = upspin.UserName(u + "+snapshot@" + domain)
	} else if suffix == "snapshot" {
		// Okay -- snapshot user is allowed to trigger snapshots.
	} else {
		s.Exitf("Only the snapshot user or the canonical user %q can trigger a snapshot", u+"@"+domain)
	}

	// Does the snapshot user exist? If not, create it.
	keyServer := s.KeyServer()
	_, err = keyServer.Lookup(snapshotUser)
	switch {
	case err == nil:
		// Okay -- user exists.
	case errors.Is(errors.NotExist, err):
		// User must be created. This should succeed because the current
		// user is either the canonical user or the snapshot user.
		err = keyServer.Put(&upspin.User{
			Name:      snapshotUser,
			Dirs:      []upspin.Endpoint{s.Config.DirEndpoint()},
			Stores:    []upspin.Endpoint{s.Config.StoreEndpoint()},
			PublicKey: s.Config.Factotum().PublicKey(),
		})
		if err != nil {
			s.Exit(err)
		}
	default:
		s.Exit(err)
	}

	// Is the root for the snapshot already created?
	_, err = s.Client.Lookup(upspin.PathName(snapshotUser), false)
	if err != nil && errors.Is(errors.NotExist, err) {
		_, err = s.Client.MakeDirectory(upspin.PathName(snapshotUser + "/"))
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
	name := path.Join(upspin.PathName(snapshotUser), "TakeSnapshot")
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Packing:    upspin.PlainPack,
		Writer:     s.Config.UserName(),
	}
	_, err = s.DirServer(entry.Name).Put(entry)
	if err != nil {
		s.Exit(err)
	}
}
