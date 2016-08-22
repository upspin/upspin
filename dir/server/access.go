// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with loading Access files and checking access permissions.

// TODO: add a cache and a negative cache for the parsed Access files.
// TODO: resolve

import (
	"upspin.io/access"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// whichAccessNoCache implements DirServer.WhichAccess without doing any
// caching of Access files.
// userLock must be held for p.User().
func (s *server) whichAccessNoCache(p path.Parsed) (*upspin.DirEntry, error) {
	const op = "dir/server.whichAccessNoCache"
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Do tree lookups (from root to path) from full path down to the root
	// until we find an Access file.
	for {
		entry, _, err := tree.Lookup(path.Join(p.Path(), "Access"))
		if err == upspin.ErrFollowLink {
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
			return nil, errors.E(op, err)
		}
		// Found the Access file.
		return entry, nil
	}
}

// whichAccess returns the DirEntry of the ruling Access file on a path or a
// DirEntry of the link if ErrFollowLink is returned.
// userLock must be held for p.User().
func (s *server) whichAccess(p path.Parsed) (*upspin.DirEntry, error) {
	const op = "dir/server.whichAccess"
	// TODO: check the cache and negcache for an access dir entry for this path.

	entry, err := s.whichAccessNoCache(p)
	if err == upspin.ErrFollowLink {
		return entry, err
	}
	if err != nil {
		// TODO: if not found, record that fact in a negative cache.
		return nil, errors.E(op, err)
	}

	// TODO: add acc to a cache.
	return entry, nil
}

// loadAccess loads and processes an Access file from its DirEntry.
func (s *server) loadAccess(entry *upspin.DirEntry) (*access.Access, error) {
	buf, err := clientutil.ReadAll(s.serverContext, entry)
	if err != nil {
		return nil, err
	}
	return access.Parse(entry.Name, buf)
}

// loadPath loads a name from the Store, if its entry can be resolved by this
// DirServer. Intended for use with access.Can. The userLock for the current
// user must be held.
func (s *server) loadPath(name upspin.PathName) ([]byte, error) {
	const op = "dir/server.loadPath"
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// TODO: relax this constraint, possibly using Client in a goroutine.
	// https://github.com/googleprivate/upspin/issues/37
	if p.User() != s.userName {
		return nil, errors.E(op, name, errors.Str("can't fetch other user's Access/Group files"))
	}
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, _, err := tree.Lookup(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return clientutil.ReadAll(s.serverContext, entry)
}

// hasRight reports whether the current user has the given right on the path.
// userLock must be held for p.User().
func (s *server) hasRight(right access.Right, p path.Parsed) (bool, error) {
	const op = "dir/server.hasRight"
	entry, err := s.whichAccess(p)
	if err == upspin.ErrFollowLink {
		// TODO: call hasRight on the link itself?
		// https://github.com/googleprivate/upspin/issues/39
		return false, upspin.ErrFollowLink
	}
	if err != nil {
		return false, errors.E(op, err)
	}
	acc, err := s.loadAccess(entry)
	if err != nil {
		return false, errors.E(op, err)
	}
	can, err := acc.Can(s.userName, right, p.Path(), s.loadPath)
	if err != nil {
		return false, errors.E(op, err)
	}
	return can, nil
}
