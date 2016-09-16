// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"time"

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
	workerInterval            = 4 * time.Hour
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
	return []snapConfig{{
		srcDir:     user + "@" + domain + "/",
		dstDir:     user + "+" + snapshotSuffix + "@" + domain + "/",
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
	users, err := tree.GlobUsers(snapshotGlob, s.logDir)
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
		cfg, err := s.getSnapshotConfig(uname, domain)
		if ferr(err) != nil {
			log.Error.Printf("%s: can't get config for user %q", op, uname+"@"+domain)
			continue
		}
		err = s.snapshotOne(cfg)
		if ferr(err) != nil {
			log.Error.Printf("%s: error snapshotting: %s", err)
		}
	}
	return firstErr
}

// snapshotOne creates a snapshot according to the snapshot configuration, if
// one is due.
func (s *server) snapshotOne(cfg *snapConfig) error {
	now := time.Now()
	date := now.UTC().Format(cfg.dateFormat)
	finalDstDir := path.Join(cfg.dstDir, date)
	entry, err := s.Lookup(finalDstDir)
	if err != nil {
		if !errors.Match(errNotExist, err) {
			// Some other error. Abort.
			return err
		}
		// Ok, proceed.
	} else {
		// Is entry too old that a new snapshot is now warranted?
		if entry.Time.Go().Add(cfg.frequency).After(now) {
			// Not time yet. Nothing to do.
			return nil
		}
		// Ok, proceed.
	}
	return s.doSnapshot(finalDstDir, entry)
}

// doSnapshot makes a snapshot from srcDir to dstDir.
func (s *server) doSnapshot(dstDir upspin.PathName, entry *upspin.DirEntry) error {
	//
}
