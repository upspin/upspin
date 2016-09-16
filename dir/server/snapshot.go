// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"time"

	"fmt"

	"strings"
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
	workerInterval            = 1 * time.Minute //4 * time.Hour
)

// snapConfig holds configuration information per snapshot. Users may have
// multiple such configurations.
type snapConfig struct {
	srcDir     upspin.PathName
	dstDir     upspin.PathName
	dateFormat string // must be formattable by time.Format.
	frequency  time.Duration
}

func (s *server) getSnapshotConfig(user, domain string) ([]snapConfig, error) {
	// TODO: only a daily snapshot of the root for now; add mechanism for
	// more options.

	// Canonicalize the username.
	idx := strings.Index(user, "+")
	if idx > 0 {
		user = user[:idx]
	}

	return []snapConfig{{
		srcDir:     upspin.PathName(user + "@" + domain + "/"),
		dstDir:     upspin.PathName(user + "+" + snapshotSuffix + "@" + domain + "/"),
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
	ticker := time.NewTicker(workerInterval)
	tickChan := ticker.C
	for {
		select {
		case <-tickChan:
			log.Printf("Tick")
			s.snapshotAll()
		case <-s.snapshotChan:
			break
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
		uname, suffix, domain, err := user.Parse(userName)
		if ferr(err) != nil {
			log.Error.Printf("%s: Parsing user: %s", op, err)
			continue
		}
		if suffix != snapshotSuffix {
			err = errors.E(errors.Internal, userName,
				errors.Errorf("globbed invalid snapshot suffix: %q", suffix))
			log.Error.Printf("%s: %s", op, err)
			ferr(err)
			continue
		}
		cfgs, err := s.getSnapshotConfig(uname, domain)
		if ferr(err) != nil {
			log.Error.Printf("%s: can't get config for user %q", op, uname+"@"+domain)
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
			return false, p, errors.E(errors.Invalid, errors.Str("cannot snapshot a path that contains a link"))
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

	// We need to impersonate this user so we can create the snapshot on
	// her/his behalf (that is, Glob and Put access permissions must work
	// as-if this user were performing the operations).
	s.userName = dstDir.User() // Ugly hack.

	err = s.makeSnapshotPath(dstDir.Path())
	if err != nil {
		return err
	}

	tree, err := s.loadTreeFor(dstDir.User())
	if err != nil {
		return err
	}

	snapEntry, err := tree.PutDir(dstDir, entry)
	if err != nil {
		return err
	}

	log.Printf("Snapshotted %q into %q", entry.SignedName, snapEntry.Name)
	return nil
}

// makeSnapshotPath makes the full path name, creating any necessary
// subdirectories. The last element of the path is guaranteed to be unique (by
// appending a  ".<version>" to it if necessary).
// userLock for the user in name must be held.
func (s *server) makeSnapshotPath(name upspin.PathName) error {
	p, err := path.Parse(name)
	if err != nil {
		return err
	}
	for i := 1; i <= p.NElem(); i++ { // start from 1: don't try to make the root.
		var exists bool
		_, exists, err = s.mkDirIfNotExist(p.First(i))
		if err != nil {
			return err
		}
		// Directory exists or was created.
		//
		// However, if this is the last element of the path, we may need
		// to create a new version of it.
		if i == p.NElem()-1 && exists {
			// List the contents and try to create the next version.
			pattern, err := path.Parse(p.Path() + "*")
			if err != nil {
				return err
			}
			entries, err := s.glob("makeSnapshotPath", pattern)
			if err != nil {
				return err
			}
			next := len(entries)
			for try := 0; try < 3; try++ {
				newP, err := path.Parse(upspin.PathName(fmt.Sprintf("%s.%d", p, next+try)))
				if err != nil {
					return err
				}
				_, exists, err = s.mkDirIfNotExist(newP)
				if err != nil {
					return err
				}
				if !exists {
					// Last directory created. Success!
					return nil
				}
			}
			return errors.E(errors.Internal, name, errors.Str("failed to make directory"))
		}
	}
	return nil
}

// mkDirIfNotExist makes a directory and returns it or reports whether it already existed.
func (s *server) mkDirIfNotExist(name path.Parsed) (*upspin.DirEntry, bool, error) {
	entry, err := s.makeDirectory("snapshotMkdir", name)
	log.Printf("making %q: err=%v", name, err)
	exists := errors.Match(errors.E(errors.Exist), err) || errors.Match(errors.E(errors.IsDir), err)
	if err != nil && !exists {
		return nil, false, err
	}
	return entry, exists, nil
}
