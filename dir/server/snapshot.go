// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strings"
	"time"

	"upspin.io/dir/server/serverlog"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

// A snapshot tree is rooted at a suffixed user name+snapshot@domain.com and
// contains directories that form the timestamp of when the snapshot was taken,
// such as bob@example.com/2017/02/12/15:45/.
//
// Snapshots are automatically taken every 12 hours.
const (
	snapshotSuffix          = "snapshot"
	snapshotControlFile     = "TakeSnapshot"
	snapshotDateFormat      = "2006/01/02/"
	snapshotTimeFormat      = "15:04"
	snapshotFullDateFormat  = snapshotDateFormat + snapshotTimeFormat
	snapshotDefaultInterval = 12 * time.Hour
	snapshotWorkerInterval  = 2 * time.Hour
)

// snapshotConfig holds the configuration for a snapshot. Users may have
// multiple such configurations.
type snapshotConfig struct {
	srcDir   upspin.PathName
	dstDir   upspin.PathName
	interval time.Duration
}

// getSnapshotConfig retrieves all configured snapshots for a user and domain
// pair, as returned by user.Parse.
func (s *server) getSnapshotConfig(userName upspin.UserName) (*snapshotConfig, error) {
	uname, suffix, domain, err := user.Parse(userName)
	if err != nil {
		return nil, err
	}
	if suffix != snapshotSuffix {
		return nil, errors.E(errors.Internal, userName,
			errors.Errorf("invalid snapshot suffix: %q", suffix))
	}

	// Strip the suffix from the username.
	uname = uname[:len(uname)-len(snapshotSuffix)-1]

	return &snapshotConfig{
		srcDir:   upspin.PathName(uname + "@" + domain + "/"),
		dstDir:   upspin.PathName(userName),
		interval: snapshotDefaultInterval,
	}, nil
}

func (s *server) startSnapshotLoop() {
	if s.snapshotControl != nil {
		log.Error.Printf("dir/server.startSnapshotLoop: attempting to restart snapshot worker")
		return
	}
	s.snapshotControl = make(chan snapshotCreate)
	go s.snapshotLoop()
}

// snapshotLoop runs in a goroutine and performs snapshots.
// We call takeSnapshotFor only from here to serialize requests with
// time-triggered snapshot.
func (s *server) snapshotLoop() {
	// Run once upon starting.
	s.snapshotAll() // returned error is already logged.

	// Run periodically.
	ticker := time.NewTicker(snapshotWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.snapshotAll() // returned error is already logged.
		case sc := <-s.snapshotControl:
			if sc.userName == "" {
				// Closing the ticker channel.
				return
			}
			sc.created <- s.takeSnapshotFor(sc.userName)
		}
	}
}

// snapshotAll scans all roots that have a +snapshot suffix, determines whether
// it's time to perform a new snapshot for them and if so snapshots them.
func (s *server) snapshotAll() error {
	const op errors.Op = "dir/server.snapshotAll"
	users, err := serverlog.ListUsersWithSuffix(snapshotSuffix, s.logDir)
	if err != nil {
		log.Error.Printf("%s: error listing snapshot users: %s", op, err)
		return err
	}
	var firstErr error
	check := func(err error) error {
		if firstErr == nil {
			firstErr = err
		}
		return err
	}
	for _, userName := range users {
		cfg, err := s.getSnapshotConfig(userName)
		if check(err) != nil {
			log.Error.Printf("%s: can't get config for user %q", op, userName)
			continue
		}
		ok, dstPath, err := s.shouldSnapshot(cfg)
		if check(err) != nil {
			log.Error.Printf("%s: error checking whether to snapshot: %s", op, err)
			continue
		}
		if !ok {
			continue
		}
		err = s.takeSnapshot(dstPath, cfg.srcDir)
		if check(err) != nil {
			log.Error.Printf("%s: error snapshotting: %s", op, err)
		}
	}
	return firstErr
}

// snapshotDir returns the destination path for a snapshot given its
// configuration.
func (s *server) snapshotDir(cfg *snapshotConfig) (path.Parsed, error) {
	date := s.now().Go().UTC().Format(snapshotDateFormat)
	dstDir := path.Join(cfg.dstDir, date)

	p, err := path.Parse(dstDir)
	if err != nil {
		return path.Parsed{}, err
	}
	return p, nil
}

// shouldSnapshot reports whether it's time to snapshot the given configuration.
// It also returns the parsed path of where the snapshot will be made.
func (s *server) shouldSnapshot(cfg *snapshotConfig) (bool, path.Parsed, error) {
	const op errors.Op = "dir/server.shouldSnapshot"

	p, err := s.snapshotDir(cfg)
	if err != nil {
		return false, path.Parsed{}, errors.E(op, err)
	}

	// List today's snapshot directory, including any suffixed snapshot.
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return false, path.Parsed{}, errors.E(op, err)
	}
	entries, _, err := tree.List(p)
	if err == upspin.ErrFollowLink {
		// We need to get the real entry and we cannot resolve links on our own.
		return false, path.Parsed{}, errors.E(op, errors.Internal, p.Path(), "cannot follow a link to snapshot")
	} else if err != nil && !errors.Is(errors.NotExist, err) {
		// Some other error. Abort.
		return false, path.Parsed{}, errors.E(op, err)
	}
	if len(entries) > 0 {
		var mostRecent time.Time
		for _, e := range entries {
			parsed, _ := path.Parse(e.Name) // can't be an error.
			t, err := time.Parse(snapshotFullDateFormat, parsed.FilePath())
			if err != nil {
				// Not a valid name. Ignore.
				continue
			}
			if t.After(mostRecent) {
				mostRecent = t
			}
		}
		// Is the last entry so old that a new snapshot is now warranted?
		if mostRecent.Add(cfg.interval).After(s.now().Go()) {
			// Not time yet. Nothing to do.
			return false, p, nil
		}
		// Ok, proceed.
	}
	return true, p, nil
}

// takeSnapshotFor takes a snapshot for a user.
// Other than in tests, it is called only from the snapshotLoop goroutine.
func (s *server) takeSnapshotFor(user upspin.UserName) error {
	cfg, err := s.getSnapshotConfig(user)
	if err != nil {
		return err
	}
	dstDir, err := s.snapshotDir(cfg)
	if err != nil {
		return err
	}
	return s.takeSnapshot(dstDir, cfg.srcDir)
}

// takeSnapshot takes a snapshot to dstDir from srcDir.
func (s *server) takeSnapshot(dstDir path.Parsed, srcDir upspin.PathName) error {
	srcParsed, err := path.Parse(srcDir)
	if err != nil {
		return err
	}
	entry, err := s.lookup(srcParsed, entryMustBeClean)
	if err != nil {
		return err
	}

	tree, err := s.loadTreeFor(dstDir.User())
	if err != nil {
		return err
	}

	timeNow := s.now().Go().UTC().Format(snapshotTimeFormat)
	dstDir, _ = path.Parse(path.Join(dstDir.Path(), timeNow))
	err = s.makeSnapshotPath(dstDir.Path())
	if err != nil {
		return err
	}

	snapEntry, err := tree.PutDir(dstDir, entry)
	if err != nil {
		return err
	}

	log.Printf("dir/server: Snapshotted %q into %q", entry.SignedName, snapEntry.Name)
	return nil
}

// makeSnapshotPath makes all directories leading up to (but not including)
// name.
func (s *server) makeSnapshotPath(name upspin.PathName) error {
	p, err := path.Parse(name)
	if err != nil {
		return err
	}
	// Traverse the path one element of a time making each subdir. We start
	// from 1 as we don't try to make the root.
	for i := 1; i < p.NElem(); i++ {
		err = s.mkDirIfNotExist(p.First(i))
		if err != nil {
			return err
		}
	}
	return nil
}

// mkDirIfNotExist makes a directory if it does not yet exist.
func (s *server) mkDirIfNotExist(name path.Parsed) error {
	// Create a new dir entry for this new dir.
	de := &upspin.DirEntry{
		Name:       name.Path(),
		SignedName: name.Path(),
		Attr:       upspin.AttrDirectory,
		Writer:     name.User(),
		Packing:    s.serverConfig.Packing(),
		Time:       upspin.Now(),
		Sequence:   0, // Tree will increment when flushed.
	}

	tree, err := s.loadTreeFor(name.User())
	if err != nil {
		return err
	}
	_, _, err = tree.Lookup(name)
	if err == upspin.ErrFollowLink {
		return errors.E(errors.Internal, "cannot mkdir through a link")
	}
	if err != nil && !errors.Is(errors.NotExist, err) {
		// Real error. Abort.
		return err
	}
	if err == nil {
		// Directory exists. We're done.
		return nil
	}
	// Attempt to put this new dir entry.
	_, err = tree.Put(name, de)
	return err
}

// isSnapshotUser reports whether the userName contains the snapshot suffix.
func isSnapshotUser(userName upspin.UserName) bool {
	// Check if the suffix is present as a way to avoid the more costly
	// user.Parse below.
	if !strings.Contains(string(userName), snapshotSuffix) {
		return false
	}
	_, suffix, _, err := user.Parse(userName)
	if err != nil {
		log.Error.Printf("dir/server.isSnapshotUser: error parsing user name %q: %s", userName, err)
		return false
	}
	return suffix == snapshotSuffix
}

// isSnapshotOwner reports whether the dialed user is the base user name
// (without the "+snapshot" suffix) of snapshotUser or the snapshotUser itself.
func (s *server) isSnapshotOwner(snapshotUser upspin.UserName) bool {
	if s.userSuffix != "" && s.userSuffix != snapshotSuffix {
		// Some other suffix. Definitely not the base user nor the
		// snapshotUser.
		return false
	}
	if s.userSuffix == snapshotSuffix {
		// userName is snapshotUser or it's another snapshot user.
		return snapshotUser == s.userName
	}
	// userName is the owner if and only if adding the snapshot suffix makes
	// it the snapshotUser.
	return s.userBase+"+"+snapshotSuffix+"@"+s.userDomain == string(snapshotUser)
}

// isSnapshotControlFile reports whether the path name is for an entry in the
// root named snapshotControlFile.
func isSnapshotControlFile(p path.Parsed) bool {
	return p.NElem() == 1 && p.Elem(0) == snapshotControlFile
}

// isValidSnapshotControlEntry reports whether an entry correctly represents the
// control entry we expect in order to start a new snapshot.
func isValidSnapshotControlEntry(entry *upspin.DirEntry) error {
	if len(entry.Blocks) != 0 || entry.IsLink() || entry.IsDir() {
		return errors.E(errors.Invalid, entry.Name, "snapshot control entry must be an empty file")
	}
	return nil
}
