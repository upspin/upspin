// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strconv"
	"strings"
	"time"

	goPath "path"

	"upspin.io/dir/server/tree"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	snapshotSuffix            = "snapshot"
	snapshotGlob              = "*+" + snapshotSuffix + "@*"
	snapshotDefaultDateFormat = "2006/01/02"
	snapshotDefaultFrequency  = 24 * time.Hour
	snapshotWorkerInterval    = 1 * time.Hour // TODO: adjust when done testing.
)

// snapConfig holds configuration information per snapshot. Users may have
// multiple such configurations.
type snapConfig struct {
	srcDir     upspin.PathName
	dstDir     upspin.PathName
	dateFormat string // must be formattable by time.Format.
	frequency  time.Duration
}

// getSnapshotConfig retrieves all configured snapshots for a user and domain
// pair, as returned by user.Parse.
func (s *server) getSnapshotConfig(userName upspin.UserName) ([]snapConfig, error) {
	uname, suffix, domain, err := user.Parse(userName)
	if err != nil {
		return nil, err
	}
	if suffix != snapshotSuffix {
		return nil, errors.E(errors.Internal, userName,
			errors.Errorf("invalid snapshot suffix: %q", suffix))
	}

	// Canonicalize the name portion of the user name.
	idx := strings.Index(uname, "+")
	if idx > 0 {
		uname = uname[:idx]
	}

	// TODO: only a daily snapshot of the root for now; add mechanism for
	// more options, such as parsing the tree for special config entries.
	return []snapConfig{{
		srcDir:     upspin.PathName(uname + "@" + domain + "/"),
		dstDir:     upspin.PathName(uname + "+" + snapshotSuffix + "@" + domain + "/"),
		dateFormat: snapshotDefaultDateFormat,
		frequency:  snapshotDefaultFrequency,
	}}, nil
}

func (s *server) startSnapshotLoop() {
	if s.snapshotChan != nil {
		log.Error.Printf("Attempting to restart snapshot worker")
		return
	}
	s.snapshotChan = make(chan bool, 1)
	go s.snapshotLoop()
}

func (s *server) stopSnapshotLoop() {
	// If the snapshotter is running, make it stop.
	if s.snapshotChan != nil {
		s.snapshotChan <- false
	}
}

// snapshotLoop runs in a goroutine and performs periodic snapshots.
func (s *server) snapshotLoop() {
	ticker := time.NewTicker(snapshotWorkerInterval)
	tickChan := ticker.C
outter:
	for {
		select {
		case <-tickChan:
			s.snapshotAll()
		case <-s.snapshotChan:
			break outter
		}
	}
	ticker.Stop()
	close(s.snapshotChan)
}

func (s *server) snapshotAll() error {
	const op = "snapshotAll"
	users, err := tree.ListUsers(snapshotGlob, s.logDir)
	if err != nil {
		return err
	}
	var firstErr error
	ferr := func(err error) error {
		if firstErr == nil {
			firstErr = err
		}
		return err
	}
	for _, userName := range users {
		cfgs, err := s.getSnapshotConfig(userName)
		if ferr(err) != nil {
			log.Error.Printf("%s: can't get config for user %q", op, userName)
			continue
		}
		for _, cfg := range cfgs {
			log.Printf("Got config: %+v", cfg)
			shouldSnapshot, dstPath, err := s.shouldSnapshot(cfg)
			if ferr(err) != nil {
				log.Error.Printf("%s: error checking whether to snapshot: %s", op, err)
				continue
			}
			if shouldSnapshot {
				err = s.takeSnapshot(dstPath, cfg.srcDir)
				if ferr(err) != nil {
					log.Error.Printf("%s: error snapshotting: %s", op, err)
				}
			}
		}
	}
	return firstErr
}

// shouldSnapshot reports whether it's time to snapshot the given configuration.
// It returns the parsed path of the final destination.
func (s *server) shouldSnapshot(cfg snapConfig) (bool, path.Parsed, error) {
	const op = "dir/server.shouldSnapshot"
	now := time.Now()
	date := now.UTC().Format(cfg.dateFormat)
	finalDstDir := path.Join(cfg.dstDir, date)

	p, err := path.Parse(finalDstDir)
	if err != nil {
		return false, p, err
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	entry, err := s.lookup(op, p, !entryMustBeClean)
	if err != nil {
		if err == upspin.ErrFollowLink {
			// We need to get the real entry and we cannot resolve links on our own.
			return false, p, errors.E(errors.Invalid, p.Path(), errors.Str("cannot follow a link to snapshot"))
		}
		if !errors.Match(errNotExist, err) {
			// Some other error. Abort.
			return false, p, err
		}
		// Ok, proceed.
	} else {
		// Is entry too old that a new snapshot is now warranted?
		if entry.Time.Go().Add(cfg.frequency).After(now) {
			// Not time yet. Nothing to do.
			return false, p, nil
		}
		// Ok, proceed.
	}
	return true, p, nil
}

// innerLookup locks the userlock and calls lookup, which does not perform
// access checks.
func (s *server) innerLookup(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	return s.lookup("innerLookup", p, entryMustBeClean)
}

// takeSnapshot takes a snapshot to dstDir from srcDir.
func (s *server) takeSnapshot(dstDir path.Parsed, srcDir upspin.PathName) error {
	entry, err := s.innerLookup(srcDir)
	if err != nil {
		return err
	}

	mu := userLock(dstDir.User())
	mu.Lock()
	defer mu.Unlock()

	tree, err := s.loadTreeFor(dstDir.User())
	if err != nil {
		return err
	}

	var createdDstDir bool
	// We only need to try twice: if the first does not succeed, the second
	// will since nextDirectorySequence ensures dstDir has a unique name.
	for tries := 0; tries < 2; tries++ {
		existed, err := s.makeSnapshotPath(dstDir.Path())
		if err != nil {
			return err
		}
		if existed {
			dstDir, err = nextDirectoryVersion(tree, dstDir)
			if err != nil {
				return err
			}
		} else {
			// Created new dir. We're done.
			createdDstDir = true
			break
		}
	}
	if !createdDstDir {
		return errors.E(errors.Internal, errors.Str("too many attempts at creating snapshot directory"))
	}

	snapEntry, err := tree.PutDir(dstDir, entry)
	if err != nil {
		return err
	}

	log.Printf("Snapshotted %q into %q", entry.SignedName, snapEntry.Name)
	return nil
}

// nextDirectoryVersion examines the tree and finds the next suitable name to
// create, by adding a monotonically-increasing version number to the original
// name. For example, if dir represents path "/2016/09/18" and under directory
// "/2016/09" there exists a "18" entry, it then would return "18.0". And if
// that existed it would return "18.1", and so on.
func nextDirectoryVersion(tree *tree.Tree, dir path.Parsed) (path.Parsed, error) {
	p := dir.Drop(1) // safe, never root.
	entries, _, err := tree.List(p)
	if err != nil {
		return path.Parsed{}, err
	}
	pattern := dir.Elem(dir.NElem()-1) + ".*"
	matches := make(map[upspin.PathName]bool)
	for _, e := range entries {
		if matched, err := goPath.Match(pattern, string(e.Name)); err != nil {
			return path.Parsed{}, errors.E(errors.Internal, err)
		} else if matched {
			matches[e.Name] = true
		}
	}
	// We try making the next name which is the count of the number of
	// matches we got. The worst case is we need to inspect all matches
	// until we're guaranteed to succeed after len(matches)+1 tries.
	for version := len(matches); version < 2*len(matches)+1; version++ {
		name := path.Join(p.Path(), dir.Elem(dir.NElem()-1)+"."+strconv.Itoa(version))
		if _, found := matches[name]; !found {
			p, err := path.Parse(name)
			if err != nil {
				return path.Parsed{}, errors.E(errors.Internal, err)
			}
			return p, nil
		}
	}
	return path.Parsed{}, errors.E(errors.Internal, errors.Str("no suitable name for next snapshot directory"))
}

// makeSnapshotPath makes the full path name, creating any necessary
// subdirectories. It reports whether the last element of the path already
// existed.
// userLock for the user in name must be held.
func (s *server) makeSnapshotPath(name upspin.PathName) (existed bool, err error) {
	var p path.Parsed
	p, err = path.Parse(name)
	if err != nil {
		return
	}
	// Traverse the path one element of a time making each subdir. We start
	// from 1 as we don't try to make the root.
	for i := 1; i <= p.NElem(); i++ {
		existed, err = s.mkDirIfNotExist(p.First(i))
		if err != nil {
			return
		}
	}
	return
}

// mkDirIfNotExist makes a directory if it does not yet exist and reports
// whether it existed already.
func (s *server) mkDirIfNotExist(name path.Parsed) (bool, error) {
	// We need to impersonate this user so we can create the snapshot on
	// their behalf (that is, Put access permissions must work
	// as-if this user were performing the operations themselves).
	s.userName = name.User() // TODO: make a superuser version mkdir instead.
	_, err := s.makeDirectory("snapshotMkdir", name)
	exists := errors.Match(errors.E(errors.Exist), err) || errors.Match(errors.E(errors.IsDir), err)
	if err != nil && !exists {
		return false, err
	}
	return exists, nil
}

// isSnapshotUser reports whether the userName contains the snapshot suffix.
func isSnapshotUser(userName upspin.UserName) bool {
	_, suffix, _, err := user.Parse(userName)
	if err != nil {
		log.Error.Printf("isSnapshotUser: error parsing user name: %q", userName)
		return false
	}
	return suffix == snapshotSuffix
}
