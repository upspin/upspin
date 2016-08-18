// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file deals with loading Access files and checking access permissions.

import (
	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// whichAccessNoCache implements DirServer.WhichAccess without doing any
// caching of Access files.
// userLock must be held for p.User().
func (s *server) whichAccessNoCache(p path.Parsed) (*upspin.DirEntry, error) {
	const op = "DirServer.WhichAccess"
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Walk the tree backwards until we find an Access file.
	for {
		if p.IsRoot() {
			// Already at the root, nothing found.
			return nil, nil
		}
		entry, _, err := tree.Lookup(path.Join(p.Path(), "Access"))
		if err == upspin.ErrFollowLink {
			return entry, err
		}
		if errors.Match(errNotExist, err) {
			continue
		}
		if err != nil {
			return nil, errors.E(op, err)
		}
		// Found the Access file.
		return entry, nil
	}

	return nil, nil
}

// loadAccess loads and processes an Access file from its DirEntry.
// TODO: No mutex is needed here. Delete this comment after the review.
func (s *server) loadAccess(accessEntry *upspin.DirEntry) (*access.Access, error) {
	return nil, nil
}
