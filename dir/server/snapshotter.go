// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"time"

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
		server:      *s,
		intervalSec: intervalSec,
	}
}

func (s *snapshotter) shouldTakeSnapshot() bool {
	return s.lastSnapshot.Add(s.intervalSec * time.Second).Before(time.Now())
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
}

// mkdir makes the full path name, creating any necessary subdirectories. The
// last element of the path is guaranteed to be unique (by appending a
// ".<version>" to it if necessary). It returns the DirEntry of the last
// element made.
func (s *snapshotter) mkdir(name upspin.PathName) (*upspin.DirEntry, error) {

}
