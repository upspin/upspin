// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with loading Access files and checking access permissions.

// TODO: add a cache and a negative cache for the parsed Access files.

import (
	"upspin.io/access"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// whichAccess implements DirServer.WhichAccess.
// userLock must be held for p.User().
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
			// If we got ErrFollowLink in the first iteration of the
			// look, it's because we're trying to look up a link,
			// say ".../link/Access". In this case, we need to go
			// one level up since the access file for the link
			// in in an ancestor of of the path.
			if entry.Name != p.Path() {
				// The link is not what we're looking up.
				return entry, upspin.ErrFollowLink
			}
			// Drop and continue (it's safe to drop because roots
			// are never links, so we're definitely not at the root.
			p = p.Drop(1)
			continue
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
// DirServer. Intended for use with access.Can. The userLock for the user in
// name must be held.
func (s *server) loadPath(name upspin.PathName) ([]byte, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(err)
	}
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(err)
	}
	entry, _, err := tree.Lookup(p)
	if err != nil {
		return nil, errors.E(err)
	}
	return clientutil.ReadAll(s.serverContext, entry)
}

// hasRight reports whether the current user has the given right on the path. If
// ErrFollowLink is returned, the DirEntry will be that of the link.
// userLock must be held for p.User().
func (s *server) hasRight(right access.Right, p path.Parsed, opts ...options) (bool, *upspin.DirEntry, error) {
	o, ss := subspan("hasRight", opts)
	defer ss.End()

	entry, err := s.whichAccess(p, o)
	if err == upspin.ErrFollowLink {
		return false, entry, upspin.ErrFollowLink
	}
	if err != nil {
		return false, nil, err
	}
	// TODO: look up in accessCache.
	var acc *access.Access
	if entry != nil {
		acc, err = s.loadAccess(entry, o)
		if err != nil {
			return false, nil, err
		}
	} else {
		// No Access file exists anywhere. Use an implicit one.
		// Get the implicit one from the defaultAccess cache.
		cacheEntry, found := s.defaultAccess.Get(p.User())
		if !found {
			// Create one now and add to the cache.
			acc, err = access.New(upspin.PathName(p.User() + "/"))
			if err != nil {
				return false, nil, err
			}
			s.defaultAccess.Add(p.User(), acc)
		} else {
			acc = cacheEntry.(*access.Access) // can't fail.
		}
	}
	can, err := acc.Can(s.userName, right, p.Path(), s.loadPath)
	if err != nil {
		return false, nil, errors.E(err)
	}
	return can, nil, nil
}
