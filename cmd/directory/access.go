package main

// This file handles parsing Access and Group files, updating the root and verifying access.

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// updateAccess handles fetching and parsing a newly-put or updated Access file and storing it in the root.
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
	root, err := d.getRoot(accessPath.User)
	if err != nil {
		return err
	}
	root.accessFiles[accessPath.Path()] = acc
	err = d.putRoot(accessPath.User, root)
	if err != nil {
		return err
	}
	return nil
}

// hasRight reports whether the user has the right on the dir entry.
func (d *dirServer) hasRight(op string, user upspin.UserName, right access.Right, pathName upspin.PathName) (bool, error) {
	parsedPathToCheck, err := path.Parse(pathName)
	if err != nil {
		return false, newDirError(op, pathName, err.Error())
	}
	root, err := d.getRoot(parsedPathToCheck.User)
	if err != nil {
		return false, err
	}

	// Now we need to find the relevant Access file. Start with the parent dir.
	accessDir := parsedPathToCheck
	for {
		accessDir = accessDir.Drop(1)

		acc, found := root.accessFiles[path.Join(accessDir.Path(), "Access")]
		if found {
			return d.checkRights(user, right, parsedPathToCheck.Path(), acc)
		}

		// If we reached the root, there's nothing else to do.
		if accessDir.IsRoot() {
			break
		}
	}
	// We did not find any Access file.
	// Instead of building logic here to deal with implicit owner rights, we make up an empty
	// access file and use that instead.
	acc, err := access.Parse(path.Join(upspin.PathName(user), "Access"), []byte(""))
	if err != nil {
		return false, fmt.Errorf("can't parse empty access file")
	}
	return d.checkRights(user, right, parsedPathToCheck.Path(), acc)
}

// checkRights is a convenience function that applies the Can method of the access entry given using the user, right and path provided.
func (d *dirServer) checkRights(user upspin.UserName, right access.Right, pathName upspin.PathName, acc *access.Access) (bool, error) {
	can, morePaths, err := acc.Can(user, right, pathName)
	if err == access.ErrNeedGroup {
		// TODO: fetch groups.
		return false, fmt.Errorf("need more groups: %+v TBD", morePaths)
	}
	logMsg.Printf("=== Access check: user %s attempting to %v file %s: allowed=%v", user, right, pathName, can)
	return can, nil
}
