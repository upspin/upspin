// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"time"

	"fmt"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// This file deals with creating automated snapshots of a user's tree.

const daily = 24 * 60 * 60

type snapshotter struct {
	server       *server
	intervalSec  int
	lastSnapshot time.Time
}

// newSnapshotter creates a new snapshotter instance taking snapshots for a user
// at the given interval (in seconds).
func (s *server) newSnapshotter(intervalSec int) *snapshotter {
	return &snapshotter{
		server:      s,
		intervalSec: intervalSec,
	}
}

func (s *snapshotter) shouldTakeSnapshot() bool {
	return s.lastSnapshot.Add(time.Duration(s.intervalSec) * time.Second).Before(time.Now())
}

// takeSnapshot creates a new snapshot of the user's tree at this point in time.
// It will create a new path, creating any necessary sub-directories and never
// overwriting any existing path in the format /snapshot/year/month/day.version
// where snapshot is the literal string, year/month/day are as expected and
// version is a monotonically increasing number to ensure uniqueness.
func (s *snapshotter) takeSnapshot() error {
	// We use s.server here, but only the exported functions, to
	// simulate simultaneous requests from the user.

	// M
	return nil
}

// mkdir makes the full path name, creating any necessary subdirectories. The
// last element of the path is guaranteed to be unique (by appending a
// ".<version>" to it if necessary). It returns the DirEntry of the last
// element made.
func (s *snapshotter) makeSnapshotPath(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	var entry *upspin.DirEntry
	for i := 1; i <= p.NElem(); i++ { // start from 1: don't try to make the root.
		var exists bool
		entry, exists, err = s.safeMkDir(p.First(i).Path())
		if err != nil {
			return nil, err
		}
		// Directory exists or was created.
		//
		// However, if this is the last element of the path, we may need
		// to create a new version of it.
		if i == p.NElem()-1 && exists {
			// List the contents and try to create the next version.
			entries, err := s.server.Glob(p.First(i).String() + "*")
			if err != nil {
				return nil, err
			}
			next := len(entries)
			for try := 0; try < 3; try++ {
				entry, exists, err = s.safeMkDir(upspin.PathName(fmt.Sprintf("%s.%d", p, next)))
				if err != nil {
					return nil, err
				}
				if !exists {
					// We're done!
					return entry, nil
				}
			}
			return nil, errors.E(errors.Internal, name, errors.Str("failed to make directory"))
		}
	}
	return entry, nil
}

// safeMkDir makes a directory and returns it or reports whether it already existed.
func (s *snapshotter) safeMkDir(name upspin.PathName) (*upspin.DirEntry, bool, error) {
	entry, err := s.server.MakeDirectory(name)
	exists := errors.Match(errors.E(errors.Exist), err) || errors.Match(errors.E(errors.IsDir), err)
	if err != nil && !exists {
		return nil, false, err
	}
	return entry, exists, nil
}
