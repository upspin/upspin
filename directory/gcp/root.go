// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file deals with encoding and decoding the user's root and caching it.

import (
	"encoding/json"

	"upspin.io/access"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// root is the user's directory root. It contains server annotations for performance and correctness.
type root struct {
	dirEntry    upspin.DirEntry
	accessFiles accessFileDB
}

// accessFileDB holds the parsed contents of Access files, indexed by their full path.
type accessFileDB map[upspin.PathName]*access.Access

// getRoot retrieves the user's root, possibly by fetching it from storage.
// It must be called with userlock held.
func (d *directory) getRoot(user upspin.UserName, opts ...options) (*root, error) {
	const op = "getRoot"
	defer span(opts).StartSpan("getRoot").End()

	userRootPath := upspin.PathName(user)

	// Is it in cache?
	if r, found := d.rootCache.Get(user); found {
		rootEntry, ok := r.(root) // Can't fail, but we check anyway to be abundantly safe.
		if !ok {
			err := newDirError(op, userRootPath, "user root cache fubar")
			log.Error.Print(err)
			return nil, err
		}
		return &rootEntry, nil
	}
	// Not in cache. Go fetch and parse it.
	buf, err := d.getCloudBytes(userRootPath)
	if err != nil {
		return nil, err
	}
	root, err := unmarshalRoot(buf)
	if err != nil {
		return nil, newDirError(op, userRootPath, err.Error())
	}
	// Put it in the cache.
	d.rootCache.Add(user, *root)
	return root, nil
}

// putRoot stores the user's root to stable storage, updating the cache.
// It must be called with userlock held.
func (d *directory) putRoot(user upspin.UserName, root *root, opts ...options) error {
	const op = "putRoot"
	defer span(opts).StartSpan("putRoot").End()

	// Put it in the root cache.
	d.rootCache.Add(user, *root)

	userRootPath := upspin.PathName(user)

	// Convert root to a savedRoot
	jsonRoot, err := marshalRoot(root)
	if err != nil {
		return newDirError(op, userRootPath, err.Error())
	}
	// Save to backend
	_, err = d.cloudClient.Put(string(userRootPath), jsonRoot)
	if err != nil {
		return newDirError(op, userRootPath, err.Error())
	}
	return nil
}

// handleRootCreation creates a root for a user.
// It must be called with any userlock held.
func (d *directory) handleRootCreation(user upspin.UserName, parsed *path.Parsed, dirEntry *upspin.DirEntry, opts ...options) error {
	const op = "Put"
	// Permission for root creation is special: only the owner can do it.
	if user != parsed.User() {
		return newDirError(op, parsed.Path(), access.ErrPermissionDenied.Error())
	}
	_, err := d.getRoot(parsed.User())
	if err != nil && err != errEntryNotFound {
		return newDirError(op, parsed.Path(), err.Error())
	}
	if err == nil {
		return newDirError(op, parsed.Path(), "directory already exists")
	}
	if !dirEntry.IsDir() {
		// We could fix this here, but let's force clients to make their requests crystal clear.
		return newDirError(op, parsed.Path(), "root is not a directory")
	}
	// Store the entry.
	root := &root{
		dirEntry:    *dirEntry,
		accessFiles: make(accessFileDB),
	}
	// We make up an empty access file to use in the default case (user has not created any Access files).
	accessPath := path.Join(upspin.PathName(parsed.User()), "Access")
	acc, err := access.New(accessPath)
	if err != nil {
		// This should never happen because accessPath has been parsed already.
		newErr := newDirError(op, parsed.Path(), err.Error())
		log.Error.Print(newErr)
		return newErr
	}
	root.accessFiles[accessPath] = acc
	err = d.putRoot(parsed.User(), root)
	if err != nil {
		return err
	}
	log.Printf("%s: %q %q", op, user, dirEntry.Name)
	return nil
}

// savedRoot is the representation of root on the storage backend.
// It does not exist in memory outside of marshalRoot and unmarshalRoot functions.
// Fields are exported so JSON can marshal and unmarshal them.
type savedRoot struct {
	// DirEntry is the dir entry.
	DirEntry upspin.DirEntry

	// AccessFiles is an accessFileDB with delayed JSON parsing.
	AccessFiles map[upspin.PathName]string
}

// unmarshalRoot takes plain JSON of a savedRoot struct and returns the root.
func unmarshalRoot(buf []byte) (*root, error) {
	var sroot savedRoot
	err := json.Unmarshal(buf, &sroot)
	if err != nil {
		return nil, err
	}
	// Now convert savedRoot to root.
	root := &root{
		dirEntry:    sroot.DirEntry,
		accessFiles: make(accessFileDB),
	}
	var firstErr error
	saveError := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for path, jsonAccess := range sroot.AccessFiles {
		acc, err := access.UnmarshalJSON(path, []byte(jsonAccess))
		if err != nil {
			saveError(err)
			continue
		}
		if _, exists := root.accessFiles[path]; exists {
			// This is bad. Our map serialization included a duplicate, which should never happen unless
			// the JSON entry on disk was modified manually or somehow strangely corrupted.
			err = newDirError("getRoot", path, "Access file duplicated in root")
			log.Error.Print(err)
			saveError(err)
		}
		root.accessFiles[path] = acc
	}
	return root, firstErr
}

// marshalRoot encodes a root as JSON.
func marshalRoot(root *root) ([]byte, error) {
	sroot := savedRoot{
		DirEntry:    root.dirEntry,
		AccessFiles: make(map[upspin.PathName]string),
	}
	var firstErr error
	saveError := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for path, acc := range root.accessFiles {
		jsonAccess, err := acc.MarshalJSON()
		if err != nil {
			saveError(err)
			log.Printf("Error marshaling access file %s: %s", path, err)
			continue
		}
		sroot.AccessFiles[path] = string(jsonAccess)
	}
	// Convert the full saved root
	jsonRoot, err := json.Marshal(sroot)
	if err != nil {
		return nil, err
	}
	return jsonRoot, nil
}
