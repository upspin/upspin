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
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	// doAccessChecks true allows the dircache to perform access checks
	// before going to the directory server. This is in preparation to
	// moving from writethrough to writeback semantics.
	//
	// TODO(p): I have set it to false to disable access checks for the
	// moment. The problem is that access checks need to be done via the
	// store cache but all other operations need to go directly to the
	// store server. Having different configs for the two methods to
	// retrieve storage blocks should have been enough. Unfortunately,
	// connection caching defeats that. Until we figure out a way to fix
	// this, we have commented out the access checks.
	doAccessChecks = false
)

// parsedAccess contains a parsed Access file and its sequence number.
type parsedAccess struct {
	a   *access.Access
	seq int64
}

// server is a SecureServer that talks to a DirServer interface and serves requests.
type server struct {
	// uncachedCfg is a config that points directly to the uncached server.
	uncachedCfg upspin.Config

	// cachedCfg is a config that points back at this dircache and storecache.
	// It exists to allow dircache to use itself and the associated
	// storecache when reading access and group files.
	cachedCfg upspin.Config

	// The on disk log.
	clog *clog

	// flushBlock is a routine to flush blocks in a writeback store.
	// TODO(p): make this less of a hack somehow
	flushBlock func(upspin.Location)

	// The directory server this dialed server should talk to.
	authority upspin.Endpoint

	// Access file cache.
	accessFiles map[upspin.PathName]parsedAccess
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(uncachedCfg, cachedCfg upspin.Config, cacheDir string, maxLogBytes int64, flushBlock func(upspin.Location)) (upspin.DirServer, error) {
	clog, err := openLog(uncachedCfg, ospath.Join(cacheDir, "dircache"), maxLogBytes)
	if err != nil {
		return nil, err
	}
	return &server{
		uncachedCfg: uncachedCfg,
		cachedCfg:   cachedCfg,
		clog:        clog,
		flushBlock:  flushBlock,
		accessFiles: make(map[upspin.PathName]parsedAccess),
	}, nil
}

// Dial implements upspin.Service.
func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s2 := *s
	s2.authority = e
	return &s2, nil
}

// dirFor returns a DirServer instance and a boolean that is true if
// path is cacheable.
func (s *server) dirFor(path upspin.PathName) (upspin.DirServer, bool, error) {
	if s.authority.Transport == upspin.Unassigned {
		return nil, false, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.uncachedCfg, s.authority)
	if err != nil {
		return nil, false, err
	}
	return dir, s.clog.cacheable(path, &s.authority), nil
}

// Lookup implements upspin.DirServer.
func (s *server) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("Lookup %q", name)

	name = path.Clean(name)
	dir, cacheable, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	if !cacheable {
		return dir.Lookup(name)
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
	dir, cacheable, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	if !cacheable {
		return dir.Glob(string(name))
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
		return nil, errors.E(entry.Name, errors.Invalid, "non-canonical name")
	}

	dir, cacheable, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	if !cacheable {
		return dir.Put(entry)
	}

	// Can we Put?
	// Note: we are currently just using granted and accErr as a check to make sure
	// we have correctly matched the access semantics of the server. In the near
	// future it will be used to allow writebacks rather than writethroughs of
	// DirEntries.
	var granted bool
	var accErr error
	if doAccessChecks {
		granted, accErr = s.canPut(name, entry)
	}

	// Wait for Access and Group block writes to flush.
	if s.flushBlock != nil && access.IsAccessControlFile(entry.Name) {
		for _, b := range entry.Blocks {
			s.flushBlock(b.Location)
		}
	}

	s.clog.globalLock.Lock()
	defer s.clog.globalLock.Unlock()

	de, err := dir.Put(entry)
	if err != nil {
		// Keep track of our access checks until we are sure they
		// match the server.
		if doAccessChecks && granted && errors.Is(errors.Permission, err) {
			log.Error.Printf("put access refused but we predicted granted: %s, %s, %s", name, s.uncachedCfg.UserName(), err)
		}
		return de, err
	}

	// If the put worked, remember it.
	if de != nil {
		entry.Sequence = de.Sequence
		s.clog.inSequence(entry.Name, entry.Sequence)
	}
	if err == nil {
		s.clog.logRequest(putReq, name, err, entry)
	}

	// If this was a Put of the root, retry the watch.
	parsed, perr := path.Parse(entry.Name)
	if perr == nil && parsed.IsRoot() {
		s.clog.retryWatch(parsed)
	}

	// Keep track of our access checks until we are sure they
	// match the server.
	if doAccessChecks && !granted {
		log.Error.Printf("put access granted but we predicted refused: %s, %s, %s", name, s.uncachedCfg.UserName(), accErr)
	}
	return de, err
}

// Delete implements upspin.DirServer.
func (s *server) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("Delete %q", name)

	name = path.Clean(name)
	dir, cacheable, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	if !cacheable {
		return dir.Delete(name)
	}

	// Can we Delete?
	// Note: we are currently just using granted and accErr as a check to make sure
	// we have correctly matched the access semantics of the server. In the near
	// future it will be used to allow writebacks rather than writethroughs of
	// DirEntries.
	var granted bool
	var accErr error
	if doAccessChecks {
		granted, accErr = s.can(name, access.Delete)
	}

	s.clog.globalLock.Lock()
	defer s.clog.globalLock.Unlock()

	de, err := dir.Delete(name)
	s.clog.logRequest(deleteReq, name, err, de)

	// Keep track of our access checks until we are sure they
	// match the server.
	if doAccessChecks {
		if granted {
			if err != nil && errors.Is(errors.Permission, err) {
				log.Error.Printf("delete access refused but we predicted granted: %s, %s, %s", name, s.uncachedCfg.UserName(), err)
			}
		} else {
			if err == nil {
				log.Error.Printf("delete access granted but we predicted refused: %s, %s, %s", name, s.uncachedCfg.UserName(), accErr)
			}
		}
	}

	return de, err
}

// WhichAccess implements upspin.DirServer.
func (s *server) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	op := logf("WhichAccess %q", name)

	name = path.Clean(name)
	dir, cacheable, err := s.dirFor(name)
	if err != nil {
		op.log(err)
		return nil, err
	}
	if !cacheable {
		return dir.WhichAccess(name)
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
	dir, _, err := s.dirFor(name)
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

// This must be called while not holding locks since it recurses into the cacheserver.
func (s *server) can(name upspin.PathName, right access.Right) (bool, error) {
	p, err := path.Parse(name)
	if err != nil {
		return false, err
	}

	var accessEntry *upspin.DirEntry

	// Creating/Deleting a root is special, don't even bother with
	// WhichAccess.
	if p.NElem() > 0 {
		// Talk to myself to determine which Access to use.
		accessEntry, err = s.WhichAccess(name)
		if err != nil {
			if err != upspin.ErrFollowLink {
				return false, err
			}
			// Put or Delete Access for a link is the same as for
			// its containing directory.
			accessEntry, err = s.WhichAccess(p.Drop(1).Path())
			if err != nil {
				return false, err
			}
		}
	}
	if accessEntry == nil {
		// No Access file. If this is the owner,
		// allow any access, otherwise no access.
		if p.User() != s.uncachedCfg.UserName() {
			return false, errors.E("not owner")
		}
		return true, nil
	}

	// Get a parsed version of it.
	acc, err := s.readAndParseAccess(accessEntry)
	if err != nil {
		return false, err
	}

	// Can we access?
	return acc.Can(s.uncachedCfg.UserName(), right, name, s.loadThroughCache)
}

// This must be called while not holding locks since it recurses into the cacheserver.
func (s *server) canPut(name upspin.PathName, de *upspin.DirEntry) (bool, error) {
	right := access.Write

	// The lookup should return a de as long as we have some rights
	// to the file. It may be incomplete but we don't care.
	ode, lookupErr := s.Lookup(name)
	if de.IsDir() {
		// Directories can only be created.
		if lookupErr == nil {
			return false, errors.E(name, errors.Exist)
		}
		if lookupErr == upspin.ErrFollowLink {
			return false, lookupErr
		}
	}

	// If the file doesn't exist, this is a creation.  If we
	// are disconnected, we may get this wrong since we won't
	// necessarily know.
	if errors.Is(errors.NotExist, lookupErr) {
		right = access.Create
	}

	switch de.Sequence {
	case upspin.SeqIgnore:
		break
	case upspin.SeqNotExist:
		if lookupErr == nil {
			return false, errors.E(name, errors.Exist)
		}
	default:
		if lookupErr != nil {
			return false, errors.E(name, errors.NotExist)
		}
		if ode.Sequence != de.Sequence {
			return false, errors.E(name, errors.Invalid, "sequence number")
		}
	}
	return s.can(name, right)
}

// readAndParseAccess returns a parsed Access file. It maintains
// a cache of parsed access files checking the current sequence
// number to make sure the parsed version is current.
//
// This must be called while not holding locks since it recurses
// into itself via the ReadAll.
//
// TODO: In the future we may need to write our own version of
// client.Get and client.ReadAll to avoid the extra overhead
// of the RPCs. However, client.Get is complex enough that
// we're doing it this way the first time around with the
// assumption that the parsed access file cache will make the
// extra overhead insignificant.
func (s *server) readAndParseAccess(de *upspin.DirEntry) (*access.Access, error) {
	// First look in cache.
	pa, ok := s.accessFiles[de.Name]
	if ok && pa.seq == de.Sequence {
		return pa.a, nil
	}

	// Not found or out of date. Fetch one.
	contents, err := clientutil.ReadAll(s.cachedCfg, de)
	if err != nil {
		return nil, err
	}

	// Parse and put into the cache.
	acc, err := access.Parse(de.Name, contents)
	if err != nil {
		return nil, err
	}
	s.accessFiles[de.Name] = parsedAccess{acc, de.Sequence}

	return acc, nil
}

// loadThroughCache retrieves the contents of a file via the caches.
func (s *server) loadThroughCache(name upspin.PathName) ([]byte, error) {
	de, err := s.Lookup(name)
	if err != nil {
		return nil, err
	}
	rv, err := clientutil.ReadAll(s.cachedCfg, de)
	return rv, err
}
