// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server // import "upspin.io/dir/server"

// This file deals with loading Access files and checking access permissions.

import (
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// accessEntry holds parsed Access files and a sequence number for their entries.
// It is the unit stored in the access cache.
type accessEntry struct {
	sequence int64          // sequence number of the DirEntry parsed.
	acc      *access.Access // parsed contents of the Access file.
}

// remoteGroupDuration is how long a remote Group can be cached.
// Modified by tests.
var remoteGroupDuration = 2 * time.Minute

// whichAccess implements DirServer.WhichAccess.
func (s *server) whichAccess(p path.Parsed, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("whichAccess", opts)
	defer ss.End()

	if isSnapshotUser(p.User()) {
		return nil, nil
	}

	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(err)
	}
	// Do tree lookups until we find an Access file. Lookups start at the
	// root and go forward till the named path. If no Access file is there,
	// pop up one level. This is so we can find the closest Acesss file,
	// while being aware of links in the way.
	for {
		accPath, err := path.Parse(path.Join(p.Path(), "Access"))
		if err != nil {
			return nil, err
		}
		entry, _, err := tree.Lookup(accPath)
		if err == upspin.ErrFollowLink {
			// WhichAccess(link) always returns the link
			// and ErrFollowLink.
			return entry, err
		}
		if errors.Is(errors.NotExist, err) {
			if p.IsRoot() {
				// Already at the root, nothing found.
				return nil, nil
			}
			p = p.Drop(1)
			continue
		}
		if err != nil {
			return nil, err
		}
		// Found the Access file.
		return entry, nil
	}
}

// loadAccess loads and processes an Access file from its DirEntry.
func (s *server) loadAccess(entry *upspin.DirEntry, opts ...options) (*access.Access, error) {
	defer span(opts).StartSpan("loadAccess").End()
	buf, err := clientutil.ReadAll(s.serverConfig, entry)
	if err != nil {
		return nil, err
	}
	return access.Parse(entry.Name, buf)
}

// loadPath loads a name from the Store, if its entry can be resolved by this
// DirServer. Intended for use with access.Can only.
func (s *server) loadPath(name upspin.PathName) ([]byte, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}

	var entry *upspin.DirEntry
	if s.userName == p.User() {
		entry, err = s.lookup(p, entryMustBeClean)
	} else {
		entry, err = s.remoteLookup(p)
		if err == nil {
			// Remember this Group file so we can then forget it
			// when it gets stale. This is guaranteed to be a Group
			// file because Access files are local only. If this
			// ever changes, we must first check whether
			// access.IsGroupFile(p.path()).
			s.remoteGroups.Add(p.Path(), lastLoad(s.now()))
		}
	}
	if err != nil {
		return nil, err
	}
	// entry contains a valid value now. Read it.
	return clientutil.ReadAll(s.serverConfig, entry)
}

// remoteLookup performs a lookup on the canonical DirServer for the path,
// which might be remote.
func (s *server) remoteLookup(p path.Parsed) (*upspin.DirEntry, error) {
	key, err := bind.KeyServer(s.serverConfig, s.serverConfig.KeyEndpoint())
	if err != nil {
		return nil, err
	}
	u, err := key.Lookup(p.User())
	if err != nil {
		return nil, err
	}
	var firstErr error
	check := func(err error) error {
		if firstErr == nil {
			firstErr = err
		}
		return err
	}
	for _, e := range u.Dirs {
		if e == s.serverConfig.DirEndpoint() {
			// It's okay to load the tree for this user, because they
			// live in this dir server, according to the KeyServer.
			return s.lookup(p, entryMustBeClean)
		}
		dir, err := bind.DirServer(s.serverConfig, e)
		if check(err) != nil {
			// Skip bad bind.
			continue
		}
		return dir.Lookup(p.Path())
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.E(errors.NotExist, p.Path(), "no remote entry for path")
}

// hasRight reports whether the current user has the given right on the path. If
// ErrFollowLink is returned, the DirEntry will be that of the link.
func (s *server) hasRight(right access.Right, p path.Parsed, opts ...options) (bool, *upspin.DirEntry, error) {
	o, ss := subspan("hasRight", opts)
	defer ss.End()

	// The owner of a snapshot has r,l rights over it and can create the
	// root, but nothing else. No one else has any rights.
	if isSnapshotUser(p.User()) {
		if s.isSnapshotOwner(p.User()) {
			switch right {
			case access.Read, access.List, access.AnyRight:
				return true, nil, nil
			case access.Create:
				return p.IsRoot(), nil, nil
			}
		}
		return false, nil, nil
	}

	entry, err := s.whichAccess(p, o)
	if err == upspin.ErrFollowLink {
		// If we need to follow a link to get to p
		// return that link.
		if entry.Name != p.Path() {
			return false, entry, err
		}
		// If p is the link then find the Access file for p.
		entry, err = s.whichAccess(p.Drop(1), o)
	}
	if err != nil {
		return false, nil, err
	}
	var acc *access.Access
	if entry != nil {
		// We have the Access file entry. Get the contents.
		acc, err = s.getAccess(entry, o)
		if err != nil {
			// There was an error acquiring or parsing this Access file.
			// That means an Access file is recorded here but is invalid,
			// at least temporarily. Instead of refusing all rights by
			// returning an error, we log the error and restore default
			// (owner-only) rights.
			log.Error.Printf("dir/server: bad Access file %q: %v; using default rights", entry.Name, err)
			acc, err = s.getDefaultAccess(p.User())
		}
	} else {
		// No Access file exists anywhere. Use an implicit one.
		// Get the implicit one from the defaultAccess cache.
		acc, err = s.getDefaultAccess(p.User())
	}
	if err != nil {
		return false, nil, err
	}
	// Finally, check whether the user has the requested right.
	can, err := acc.Can(s.userName, right, p.Path(), s.loadPath)
	if err != nil {
		return false, nil, err
	}
	return can, nil, nil
}

// getAccess returns the parsed contents of the Access file described by entry.
func (s *server) getAccess(entry *upspin.DirEntry, opts ...options) (*access.Access, error) {
	o, ss := subspan("getAccess", opts)
	defer ss.End()

	// Sanity check: is this really an Access file?
	if !access.IsAccessFile(entry.Name) {
		return nil, errors.E(errors.Internal, entry.Name, "not an Access file")
	}

	// Is it in the cache?
	var accEntry *accessEntry
	a, found := s.access.Get(entry.Name)
	if found {
		var ok bool
		accEntry, ok = a.(*accessEntry)
		if !ok {
			return nil, errors.E(errors.Internal, "invalid accessEntry")
		}
		if entry.Sequence == accEntry.sequence {
			return accEntry.acc, nil
		}
		// A race happened and we have a cached version that is
		// different than the requested one. Fall through and load the
		// requested one.
	}
	// Not in cache; load data from the Store.

	acc, err := s.loadAccess(entry, o)
	if err != nil {
		return nil, err
	}
	// Add or update cache.
	s.access.Add(entry.Name, &accessEntry{
		sequence: entry.Sequence,
		acc:      acc,
	})
	return acc, nil
}

// getDefaultAccess returns the implicit Access file for a user.
func (s *server) getDefaultAccess(userName upspin.UserName) (acc *access.Access, err error) {
	cacheEntry, found := s.defaultAccess.Get(userName)
	if !found {
		// Create one now and add to the cache.
		acc, err = access.New(upspin.PathName(userName + "/"))
		if err != nil {
			return
		}
		s.defaultAccess.Add(userName, acc)
	} else {
		var ok bool
		acc, ok = cacheEntry.(*access.Access)
		if !ok {
			return nil, errors.E(errors.Internal, "not an Access file")
		}
	}
	return
}

// loadGroup loads a group file from its entry and parses it, but does not
// pass it to access.AddGroup
func (s *server) loadGroup(p path.Parsed, entry *upspin.DirEntry) error {
	data, err := clientutil.ReadAll(s.serverConfig, entry)
	if err != nil {
		return err
	}
	_, err = access.ParseGroup(p, data)
	return err
}

// groupRefreshLoop periodically removes potentially stale Group files from the
// access group cache. It runs continuously and must be in a goroutine.
func (s *server) groupRefreshLoop() {
	for {
		time.Sleep(remoteGroupDuration)
		for {
			k, v := s.remoteGroups.PeekOldest()
			if k == nil || v == nil {
				// Nothing to do.
				break
			}
			lastLoaded, ok := v.(lastLoad)
			if !ok {
				log.Error.Printf("dir/server.groupRefreshLoop: value is not of type lastLoad")
				return
			}
			expiration := upspin.Time(lastLoaded) + upspin.Time(remoteGroupDuration.Seconds())
			if expiration < s.now() {
				// Remote the oldest (LRU) and calls OnEviction.
				key, _ := s.remoteGroups.RemoveOldest()
				lastLoaded.OnEviction(key)
				continue // look for the next one to expire.
			}
			break // Oldest entry is not old enough.
		}
	}
}

// lastLoad represents the time a remote Group file was loaded by the DirServer.
// It is the value stored in the remoteGroup cache.
type lastLoad upspin.Time

// OnEviction implements cache.EvictionNotifier. It is called when the remote
// group cache is full or an item is forcefully evicted (by calling RemoveOldest
// on the cache). In effect, this "forgets" the Group file if it was loaded.
func (l lastLoad) OnEviction(key interface{}) {
	name, ok := key.(upspin.PathName)
	if !ok {
		log.Error.Printf("dir/server: key in remote group cache is not a pathname: %v", key)
		return
	}
	access.RemoveGroup(name) // ignore return, it may not have been loaded.
}
