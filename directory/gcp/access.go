// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file handles parsing Access and Group files, updating the root and verifying access.

import (
	"errors"

	"upspin.io/access"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// updateAccess handles fetching and parsing a new or updated Access file and caches its parsed representation in root.accessFiles.
// It must NOT be called with user lock held.
func (d *directory) updateAccess(accessPath *path.Parsed, location *upspin.Location) error {
	buf, err := d.storeGet(location)
	if err != nil {
		return err
	}
	acc, err := access.Parse(accessPath.Path(), buf)
	if err != nil {
		// access.Parse already sets the path, no need to duplicate it here.
		return newDirError("UpdateAccess", "", err.Error())
	}

	user := accessPath.User()

	// Hold the user lock.
	mu := userLock(user)
	mu.Lock()
	defer mu.Unlock()

	root, err := d.getRoot(user)
	if err != nil {
		return err
	}
	root.accessFiles[accessPath.Path()] = acc
	err = d.putRoot(user, root)
	if err != nil {
		return err
	}
	return nil
}

// deleteAccess removes the contents of an Access file from the root.
// It must be called with user lock held.
func (d *directory) deleteAccess(accessPath *path.Parsed) error {
	user := accessPath.User()
	root, err := d.getRoot(user)
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
	return d.putRoot(user, root)
}

// hasRight reports whether the user has the right on the path. It's assumed that all prior verifications have taken
// place, such as verifying whether the user is writing to a file that existed as a directory or vice-versa, etc.
// It must NOT be called with user lock held.
func (d *directory) hasRight(op string, user upspin.UserName, right access.Right, parsedPath *path.Parsed) (bool, error) {
	_, acc, err := d.whichAccess(op, parsedPath)
	if err != nil {
		return false, err
	}
	return d.checkRights(user, right, parsedPath.Path(), acc)
}

// whichAccess returns the path name and the parsed contents of the ruling Access file for a given path name.
// It must NOT be called with user lock held.
// TODO: we should cache this computation as it requires parsing paths, traversing them, doing drop, joins, etc.
func (d *directory) whichAccess(op string, parsedPath *path.Parsed) (upspin.PathName, *access.Access, error) {
	user := parsedPath.User()

	// Hold the user lock.
	mu := userLock(user)
	mu.Lock()
	defer mu.Unlock()

	root, err := d.getRoot(user)
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
				_, err := d.getNonRoot(accessPath)
				if err == errEntryNotFound {
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
	err = errors.New("No Access file found anywhere")
	log.Error.Print(err)
	return "", nil, err
}

// checkRights is a convenience function that applies the Can method of the access entry given using the user, right and path provided.
func (d *directory) checkRights(user upspin.UserName, right access.Right, pathName upspin.PathName, acc *access.Access) (bool, error) {
	can, err := acc.Can(user, right, pathName, d.load)
	log.Printf("Access check: user %s attempting to %v file %s: allowed=%v [err=%v]", user, right, pathName, can, err)
	return can, err
}

// load is a helper for Access.Can that gets the entire contents of the named item.
// It must NOT be called with path lock held.
func (d *directory) load(pathName upspin.PathName) ([]byte, error) {
	mu := pathLock(pathName)
	mu.Lock()
	defer mu.Unlock()

	dirEntry, err := d.getNonRoot(pathName)
	if err != nil {
		return nil, err
	}
	return d.storeGet(&dirEntry.Location)
}
