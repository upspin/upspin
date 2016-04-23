package main

// This file handles parsing Access and Group files, updating the root and verifying access.

import (
	"errors"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// updateAccess handles fetching and parsing a new or updated Access file and caches its parsed representation in root.accessFiles.
func (d *dirServer) updateAccess(accessPath *path.Parsed, location *upspin.Location) error {
	buf, err := d.storeClient.Get(location)
	if err != nil {
		return err
	}
	acc, err := access.Parse(accessPath.Path(), buf)
	if err != nil {
		// access.Parse already sets the path, no need to duplicate it here.
		return newDirError("UpdateAccess", "", err.Error())
	}
	root, err := d.getRoot(accessPath.User())
	if err != nil {
		return err
	}
	root.accessFiles[accessPath.Path()] = acc
	err = d.putRoot(accessPath.User(), root)
	if err != nil {
		return err
	}
	return nil
}

// deleteAccess removes the contents of an Access file from the root.
func (d *dirServer) deleteAccess(accessPath *path.Parsed) error {
	root, err := d.getRoot(accessPath.User())
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
	return d.putRoot(accessPath.User(), root)
}

// hasRight reports whether the user has the right on the path. It's assumed that all prior verifications have taken
// place, such as verifying whether the user is writing to a file that existed as a directory or vice-versa, etc.
func (d *dirServer) hasRight(op string, user upspin.UserName, right access.Right, parsedPath *path.Parsed) (bool, error) {
	_, acc, err := d.whichAccess(op, parsedPath)
	if err != nil {
		return false, err
	}
	return d.checkRights(user, right, parsedPath.Path(), acc)
}

// whichAccess returns the path name and the parsed contents of the ruling Access file for a given path name.
// TODO: we should cache this computation as it requires a parsing paths, traversing them, doing drop, joins, etc.
func (d *dirServer) whichAccess(op string, parsedPath *path.Parsed) (upspin.PathName, *access.Access, error) {
	root, err := d.getRoot(parsedPath.User())
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
				_, err := d.getNonRoot(accessPath)
				if err == errEntryNotFound {
					return "", acc, nil // The Access file does not exist.
				}
				return accessPath, acc, err
			}
			logMsg.Printf("Found access file in %s: %+v ", accessPath, acc)
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
	logErr.Printf("WARN: %s", err)
	return "", nil, err
}

// checkRights is a convenience function that applies the Can method of the access entry given using the user, right and path provided.
func (d *dirServer) checkRights(user upspin.UserName, right access.Right, pathName upspin.PathName, acc *access.Access) (bool, error) {
	var groupErr error
	for {
		can, morePaths, err := acc.Can(user, right, pathName)
		if err == access.ErrNeedGroup {
			for _, g := range morePaths {
				err = d.addGroup(g, acc)
				if err != nil {
					if groupErr == nil {
						groupErr = err
					}
				}
			}
			if groupErr != nil {
				logErr.Printf("Error checking access: %s", groupErr)
				return false, groupErr
			}
			continue // Try acc.Can again
		}
		logMsg.Printf("Access check: user %s attempting to %v file %s: allowed=%v [err=%v]", user, right, pathName, can, err)
		return can, err
	}
}

// addGroup looks up a Group name, fetches its contents if found and calls access.AddGroup with the contents.
// It is currently limited to group files that belong to this directory service (that is, it does not attempt to dial
// another directory service to find it).
func (d *dirServer) addGroup(pathName upspin.PathName, acc *access.Access) error {
	dirEntry, err := d.getNonRoot(pathName)
	if err != nil {
		return err
	}
	buf, err := d.storeClient.Get(&dirEntry.Location)
	if err != nil {
		// This will happen if we're not the Endpoint for the Location.
		// TODO: figure out our location -- this is subtle given our IP address may not match our
		// public DNS record and there might be multiple addresses bound to this server.
		return err
	}
	return access.AddGroup(pathName, buf)
}
