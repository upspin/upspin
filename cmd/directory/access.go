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

// deleteAccess removes the contents of an Access file from the root.
func (d *dirServer) deleteAccess(accessPath *path.Parsed) error {
	root, err := d.getRoot(accessPath.User)
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
	return d.putRoot(accessPath.User, root)
}

// hasRight reports whether the user has the right on the path. It's assumed that all prior verifications have taken
// place, such as verifying whether the user is writing to a file that existed as a directory or vice-versa, etc.
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
		if !accessDir.IsRoot() {
			accessDir = accessDir.Drop(1)
		}

		accessPath := path.Join(accessDir.Path(), "Access")
		acc, found := root.accessFiles[accessPath]
		if found {
			logMsg.Printf("Found access file in %s: %+v ", accessPath, acc)
			return d.checkRights(user, right, parsedPathToCheck.Path(), acc)
		}

		// If we reached the root, there's nothing else to do.
		if accessDir.IsRoot() {
			break
		}
	}
	// We did not find any Access file. The root should have an implicit one. This is an error.
	return false, errors.New("No Access file found anywhere")
}

// checkRights is a convenience function that applies the Can method of the access entry given using the user, right and path provided.
func (d *dirServer) checkRights(user upspin.UserName, right access.Right, pathName upspin.PathName, acc *access.Access) (bool, error) {
	for retries := 0; retries < 10; retries++ {
		can, morePaths, err := acc.Can(user, right, pathName)
		if err == access.ErrNeedGroup {
			groupErr := d.addAllGroups(morePaths, acc)
			if groupErr != nil {
				return false, groupErr
			}
			continue // Try acc.Can again
		}
		if err != nil {
			return false, err
		}
		logMsg.Printf("Access check: user %s attempting to %v file %s: allowed=%v [err=%v]", user, right, pathName, can, err)
		return can, nil
	}
	return false, newDirError("checkRights", pathName, "too many retries parsing Access files")
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

// addAllGroups attempts to add all given group path names into acc by calling addGroup for each entry of groups.
func (d *dirServer) addAllGroups(groups []upspin.PathName, acc *access.Access) error {
	var groupErr error
	for _, g := range groups {
		err := d.addGroup(g, acc)
		if err != nil {
			if groupErr == nil {
				groupErr = err
			}
		}
	}
	return groupErr
}

// generateWrappingRequest generates two lists (as slices): 1) all the path names that need to
// be "shared" (made readable) and 2) the user names that must be able to read the first list.
func (d *dirServer) generateWrappingRequest(accessParsedPath *path.Parsed) ([]upspin.PathName, []upspin.UserName, error) {
	// TODO: we should keep track of the user's longest path name. But the depth being unbounded is something that
	// opens us up to DoS attacks. But so is the fan-out (number of entries per dir). Oh well.
	const maxDepth = 10
	paths, err := d.cloudClient.ListPrefix(accessParsedPath.Drop(1).String()+"/", maxDepth)
	if err != nil {
		return nil, nil, err
	}
	// Now get the Access file and retrieve all readers from it.
	root, err := d.getRoot(accessParsedPath.User)
	if err != nil {
		return nil, nil, err
	}
	acc, found := root.accessFiles[accessParsedPath.Path()]
	if !found {
		return nil, nil, newDirError("genWork", accessParsedPath.Path(), "no such Access file")
	}
	var readers []upspin.UserName
	var morePaths []upspin.PathName
	var groupErr error
	for retries := 0; retries < 10; retries++ {
		readers, morePaths, err = acc.UserNames(access.Read)
		if err == access.ErrNeedGroup {
			groupErr = d.addAllGroups(morePaths, acc)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		break
	}
	if groupErr != nil {
		return nil, nil, groupErr
	}
	// Convert string paths to upspin.PathName
	pathNames := make([]upspin.PathName, len(paths))
	for i, p := range paths {
		pathNames[i] = upspin.PathName(p)
	}
	return pathNames, readers, nil
}
