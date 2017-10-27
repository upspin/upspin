// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircache

import (
	"fmt"
	ospath "path"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// server is a SecureServer that talks to a DirServer interface and serves requests.
type server struct {
	cfg upspin.Config

	// The on disk log.
	clog *clog

	// flushBlock is a routine to flush blocks in a writeback store.
	// TODO(p): make this less of a hack somehow
	flushBlock func(upspin.Location)

	// The directory server this dialed server should talk to.
	authority upspin.Endpoint
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(cfg upspin.Config, cacheDir string, maxLogBytes int64, flushBlock func(upspin.Location)) (upspin.DirServer, error) {
	clog, err := openLog(cfg, ospath.Join(cacheDir, "dircache"), maxLogBytes)
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:        cfg,
		clog:       clog,
		flushBlock: flushBlock,
	}, nil
}

// Dial implements upspin.Service.
func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s2 := *s
	s2.authority = e
	return &s2, nil
}

// dirFor returns a DirServer instance.
func (s *server) dirFor(path upspin.PathName) (upspin.DirServer, error) {
	if s.authority.Transport == upspin.Unassigned {
		return nil, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.cfg, s.authority)
	if err == nil {
		s.clog.proxyFor(path, &s.authority)
	}
	return dir, err
}

// Lookup implements upspin.DirServer.
func (s *server) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("Lookup %q", name)

	name = path.Clean(name)
	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}

	s.clog.globalLock.RLock()
	defer s.clog.globalLock.RUnlock()

	if de, err, ok := s.clog.lookup(name); ok {
		if err == nil && de != nil && de.Attr == upspin.AttrLink {
			err = upspin.ErrFollowLink
		}
		return de, err
	}

	de, err := dir.Lookup(name)
	s.clog.logRequest(lookupReq, name, err, de)

	return de, err
}

// Glob implements upspin.DirServer.
func (s *server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	op := logf("Glob %q", pattern)

	name := path.Clean(upspin.PathName(pattern))
	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}

	s.clog.globalLock.RLock()
	defer s.clog.globalLock.RUnlock()

	if entries, err, ok := s.clog.lookupGlob(name); ok {
		return entries, err
	}

	entries, globReqErr := dir.Glob(string(name))
	s.clog.logGlobRequest(name, globReqErr, entries)

	return entries, globReqErr
}

// Put implements upspin.DirServer.
// TODO(p): Remember access errors to avoid even trying?
func (s *server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	op := logf("Put %q", entry.Name)
	name := path.Clean(entry.Name)
	if name != entry.Name {
		return nil, errors.E(entry.Name, errors.Invalid, errors.Str("non-canonical name"))
	}

	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}

	// Since the directory server needs to read the Access/Group file
	// we need to ensure that it is flushed from any cache before the Put.
	if s.flushBlock != nil && access.IsAccessControlFile(entry.Name) {
		for _, b := range entry.Blocks {
			s.flushBlock(b.Location)
		}
	}

	s.clog.globalLock.Lock()
	defer s.clog.globalLock.Unlock()

	de, err := dir.Put(entry)
	if err == nil {
		// If the put worked, remember it.
		if de != nil {
			entry.Sequence = de.Sequence
		}
		s.clog.logRequest(putReq, name, err, entry)

		// If this was a Put of the root, retry the watch.
		parsed, perr := path.Parse(entry.Name)
		if perr == nil && parsed.IsRoot() {
			s.clog.retryWatch(parsed)
		}
	}

	return de, err
}

// Delete implements upspin.DirServer.
func (s *server) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("Delete %q", name)

	name = path.Clean(name)
	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}

	s.clog.globalLock.Lock()
	defer s.clog.globalLock.Unlock()

	de, err := dir.Delete(name)
	s.clog.logRequest(deleteReq, name, err, de)

	return de, err
}

// WhichAccess implements upspin.DirServer.
func (s *server) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("WhichAccess %q", name)

	name = path.Clean(name)
	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}

	s.clog.globalLock.RLock()
	defer s.clog.globalLock.RUnlock()

	if de, err, ok := s.clog.whichAccess(name); ok {
		return de, err
	}
	de, err := dir.WhichAccess(name)
	s.clog.logRequest(whichAccessReq, name, err, de)

	return de, err
}

// Watch implements upspin.DirServer.
func (s *server) Watch(name upspin.PathName, sequence int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	op := logf("Watch %q", name)

	name = path.Clean(name)
	dir, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	return dir.Watch(name, sequence, done)
}

func (s *server) Endpoint() upspin.Endpoint { return s.authority }
func (s *server) Close()                    {}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Debug.Print("dir/dircache: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
}
