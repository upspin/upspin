// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with loading Access files and checking access permissions.

import (
	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// whichAccess implements DirServer.WhichAccess.
func (s *server) whichAccess(p path.Parsed, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("whichAccess", opts)
	defer ss.End()

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
		if errors.Match(errNotExist, err) {
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
	buf, err := clientutil.ReadAll(s.serverContext, entry)
	if err != nil {
		return nil, err
	}
	return access.Parse(entry.Name, buf)
}

// loadPath loads a name from the Store, if its entry can be resolved by this
// DirServer. Intended for use with access.Can.
func (s *server) loadPath(name upspin.PathName) ([]byte, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}

	var entry *upspin.DirEntry
	if s.userName == p.User() {
		entry, err = s.lookup("loadPath", p, !entryMustBeClean)
	} else {
		entry, err = s.remoteLookup(p)
	}
	if err != nil {
		return nil, err
	}
	// entry contains a valid value now. Read it.
	return clientutil.ReadAll(s.serverContext, entry)
}

// remoteLookup performs a lookup on the canonical DirServer for the path,
// which might be remote.
func (s *server) remoteLookup(p path.Parsed) (*upspin.DirEntry, error) {
	key, err := bind.KeyServer(s.serverContext, s.serverContext.KeyEndpoint())
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
		if e == s.serverContext.DirEndpoint() {
			// It's okay to load the tree for this user, because they
			// live in this dir server, according to the KeyServer.
			return s.lookup("remoteLookup", p, !entryMustBeClean)
		}
		dir, err := bind.DirServer(s.serverContext, e)
		if check(err) != nil {
			// Skip bad bind.
			continue
		}
		return dir.Lookup(p.Path())
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.E(errors.NotExist, p.Path(), errors.Str("no remote entry for path"))
}

// hasRight reports whether the current user has the given right on the path. If
// ErrFollowLink is returned, the DirEntry will be that of the link.
func (s *server) hasRight(right access.Right, p path.Parsed, opts ...options) (bool, *upspin.DirEntry, error) {
	o, ss := subspan("hasRight", opts)
	defer ss.End()

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
		return nil, errors.E(errors.Internal, entry.Name, errors.Str("not an Access file"))
	}

	// Is it in the cache?
	if acc, found := s.access.Get(entry.Name); found {
		if a, ok := acc.(*access.Access); ok {
			return a, nil
		}
		return nil, errors.E(errors.Internal, errors.Str("invalid accessCache entry"))
	}
	// Not in cache, load from the Store.
	// TODO: we should also reload any known Group files from their remote
	// origin from time to time if they're not local to this server.
	acc, err := s.loadAccess(entry, o)
	if err != nil {
		return nil, err
	}
	// Add to the cache.
	s.access.Add(entry.Name, acc)
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
			return nil, errors.E(errors.Internal, errors.Str("not an Access file"))
		}
	}
	return
}
