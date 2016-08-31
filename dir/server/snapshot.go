// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with creating automated snapshots of a user's tree.

import (
	"fmt"
	"time"

	"upspin.io/dir/server/tree"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// daily is the default period for taking snapshots.
const daily = 24 * time.Hour

// snapshotter helps the server run a goroutine per tree, taking snapshots
// periodically.
type snapshotter struct {
	server       *server
	interval     time.Duration
	lastSnapshot time.Time
	ch           chan bool
}

// newSnapshotter creates a new snapshotter instance taking snapshots for a user
// at the given interval (in seconds).
func (s *server) newSnapshotter(interval time.Duration) *snapshotter {
	ss := &snapshotter{
		server:   s,
		interval: interval,
		ch:       make(chan bool),
	}
	go ss.snapshotRoutine()
	return ss
}

// snapshotRoutine runs in a goroutine taking snapshots periodically.
// TODO: exit goroutine when server is Closed.
// TODO: when first starting up, find out when the last snapshot was and
// take one posthaste if it's time for a new one now.
func (s *snapshotter) snapshotRoutine() {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		<-t.C
		err := s.server.takeSnapshot()
		if err != nil {
			log.Error.Printf("snapshotRoutine: error taking snapshot: %s", err)
		}
	}
}

// takeSnapshot creates a new snapshot of the user's tree. It creates a new
// path, creating any necessary sub-directories and never overwriting any
// existing path in the format /snapshot/year/month/day.version
// where snapshot is the literal string, year/month/day are as expected and
// version is a monotonically increasing number to ensure uniqueness.
func (s *server) takeSnapshot() error {
	user := s.userName
	date := time.Now().UTC().Format(tree.SnapshotDateFormat)
	name := upspin.PathName(fmt.Sprintf("%s/%s/%s", user, tree.SnapshotPrefix, date))
	entry, err := s.makeSnapshotPath(name)
	if err != nil {
		return err
	}
	// Make entry point to the current root.
	root, err := s.Lookup(upspin.PathName(user) + "/")
	if err != nil {
		return err
	}
	newEntry := *root // Make a copy and then deep copy it.
	newEntry.Blocks = append([]upspin.DirBlock{}, root.Blocks...)
	newEntry.Packdata = append([]byte{}, root.Packdata...)
	newEntry.Name = entry.Name // Re-instate the name.

	// Now we must Put this back directly into the tree.
	mu := userLock(user)
	mu.Lock()
	defer mu.Unlock()

	tree, err := s.loadTreeFor(user)
	if err != nil {
		return err
	}
	p, err := path.Parse(newEntry.Name)
	if err != nil {
		return err
	}
	_, err = tree.Put(p, &newEntry)
	return err
}

// makeSnapshotPath makes the full path name, creating any necessary
// subdirectories. The last element of the path is guaranteed to be unique (by
// appending a  ".<version>" to it if necessary). It returns the DirEntry of the
// last element made.
func (s *server) makeSnapshotPath(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	var entry *upspin.DirEntry
	for i := 1; i <= p.NElem(); i++ { // start from 1: don't try to make the root.
		var exists bool
		entry, exists, err = s.mkDirIfNotExist(p.First(i).Path())
		if err != nil {
			return nil, err
		}
		// Directory exists or was created.
		//
		// However, if this is the last element of the path, we may need
		// to create a new version of it.
		if i == p.NElem()-1 && exists {
			// List the contents and try to create the next version.
			entries, err := s.Glob(p.String() + "*")
			if err != nil {
				return nil, err
			}
			next := len(entries)
			for try := 0; try < 3; try++ {
				entry, exists, err = s.mkDirIfNotExist(upspin.PathName(fmt.Sprintf("%s.%d", p, next+try)))
				if err != nil {
					return nil, err
				}
				if !exists {
					// Last directory created. Success!
					return entry, nil
				}
			}
			return nil, errors.E(errors.Internal, name, errors.Str("failed to make directory"))
		}
	}
	return entry, nil
}

// mkDirIfNotExist makes a directory and returns it or reports whether it already existed.
func (s *server) mkDirIfNotExist(name upspin.PathName) (*upspin.DirEntry, bool, error) {
	entry, err := s.MakeDirectory(name)
	exists := errors.Match(errors.E(errors.Exist), err) || errors.Match(errors.E(errors.IsDir), err)
	if err != nil && !exists {
		return nil, false, err
	}
	return entry, exists, nil
}
