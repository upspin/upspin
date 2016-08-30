// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with loading Access files and checking access permissions.

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
	// TODO: must check whether p.User() is the one originally locked and if
	// not, we must lock it in a goroutine or prove that this read-race is
	// harmless. Allowing a read-race for now.
	// https://github.com/googleprivate/upspin/issues/37
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
		// We have more work to do. We need to check whether the user
		// has Any right on the link itself.
		linkPath, err := path.Parse(entry.Name)
		if err != nil {
			return false, nil, err
		}
		if hasAny, _, err := s.hasRight(access.AnyRight, linkPath, o); err != nil {
			// Some error other than ErrFollowLink.
			return false, nil, err
		} else if hasAny {
			// User has Any right on the link. Let them follow it.
			return false, entry, upspin.ErrFollowLink
		}
		// Denied. User has no right on link. Pretend it doesn't exist.
		return false, nil, errors.E(p.Path(), errors.NotExist)
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
	can, err := acc.Can(s.userName, right, p.Path(), s.loadPath)
	if err != nil {
		return false, nil, errors.E(err)
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
