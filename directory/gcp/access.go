// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file handles parsing Access and Group files, updating the root and verifying access.

import (
	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// updateAccess handles fetching and parsing a new or updated Access file and caches its parsed representation in root.accessFiles.
// It must be called with userlock held.
func (d *directory) updateAccess(accessPath *path.Parsed, location *upspin.Location, opts ...options) error {
	defer span(opts).StartSpan("updateAccess").End()
	buf, err := d.storeGet(location)
	if err != nil {
		return err
	}
	acc, err := access.Parse(accessPath.Path(), buf)
	if err != nil {
		// access.Parse already sets the path, no need to duplicate it here.
		return errors.E("UpdateAccess", err)
	}

	user := accessPath.User()
	root, err := d.getRoot(user, opts...)
	if err != nil {
		return err
	}
	root.accessFiles[accessPath.Path()] = acc
	err = d.putRoot(user, root, opts...)
	if err != nil {
		return err
	}
	return nil
}

// deleteAccess removes the contents of an Access file from the root.
// It must be called with userlock held.
func (d *directory) deleteAccess(accessPath *path.Parsed, opts ...options) error {
	defer span(opts).StartSpan("deleteAccess").End()
	user := accessPath.User()
	root, err := d.getRoot(user, opts...)
	if err != nil {
		return err
	}
	path := accessPath.Path()
	delete(root.accessFiles, path)
	// Is this Access file the one at the root? If so, replace it with a default one.
	if accessPath.Drop(1).IsRoot() {
		root.accessFiles[path], err = access.New(path)
		if err != nil {
			return err
		}
	}
	return d.putRoot(user, root, opts...)
}

// hasRight reports whether the user has the right on the path. It's assumed that all prior verifications have taken
// place, such as verifying whether the user is writing to a file that existed as a directory or vice-versa, etc.
// It must be called with userlock held.
func (d *directory) hasRight(op string, user upspin.UserName, right access.Right, parsedPath *path.Parsed, opts ...options) (bool, error) {
	_, acc, err := d.whichAccess(op, parsedPath, opts...)
	if err != nil {
		return false, err
	}
	return d.checkRights(user, right, parsedPath.Path(), acc, opts...)
}

// whichAccess returns the path name and the parsed contents of the ruling Access file for a given path name.
// It must be called with userlock held.
// TODO: we should cache this computation as it requires parsing paths, traversing them, doing drop, joins, etc.
func (d *directory) whichAccess(op string, parsedPath *path.Parsed, opts ...options) (upspin.PathName, *access.Access, error) {
	defer span(opts).StartSpan("whichAccess").End()
	user := parsedPath.User()
	root, err := d.getRoot(user, opts...)
	if err != nil {
		return "", nil, err
	}
	// Find the relevant Access file. Start with the parent dir.
	accessDir := *parsedPath
	for {
		accessPath := path.Join(accessDir.Path(), "Access")
		acc, found := root.accessFiles[accessPath]
		if found {
			// If it's the root one, verify the pathname actually exists.
			if accessDir.IsRoot() {
				// Not locking the path here as this is just to check existence of it and it's
				// racy by nature (i.e. the lock wouldn't prevent races as soon as it's released).
				_, err := d.getNonRoot(accessPath, opts...)
				if isErrNotExist(err) {
					return "", acc, nil // The Access file does not exist.
				}
				return accessPath, acc, err
			}
			log.Printf("Found access file in %s: %+v ", accessPath, acc)
			return accessPath, acc, nil
		}

		// If we reached the root, there's nothing else to do.
		if accessDir.IsRoot() {
			break
		}

		accessDir = accessDir.Drop(1)
	}
	// We did not find any Access file. The root should have an implicit one. This is a serious error.
	err = errors.E("whichAccess", errors.NotExist, "No Access file found anywhere")
	log.Error.Print(err)
	return "", nil, err
}

// checkRights is a convenience function that applies the Can method of the access entry given using the user, right and path provided.
// It must be called with userlock held.
func (d *directory) checkRights(user upspin.UserName, right access.Right, pathName upspin.PathName, acc *access.Access, opts ...options) (bool, error) {
	defer span(opts).StartSpan("checkRights").End()
	can, err := acc.Can(user, right, pathName, d.load)
	log.Printf("Access check: user %s attempting to %v file %s: allowed=%v [err=%v]", user, right, pathName, can, err)
	return can, err
}

// load is a helper for Access.Can that gets the entire contents of the named item.
// It must be called with userlock held.
func (d *directory) load(pathName upspin.PathName) ([]byte, error) {
	dirEntry, err := d.getNonRoot(pathName)
	if err != nil {
		return nil, err
	}
	return d.storeGet(&dirEntry.Location)
}
