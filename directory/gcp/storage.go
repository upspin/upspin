// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file deals with low-level storage of dir entries on GCP, their mashaling and unmarshaling and caching.

// TODO: we only have one server, so we know when the cache is invalid which is never, since we update it every time
// we update the backend. But this won't be true if we ever have more than one server serving a given user. At that point,
// we need to subscribe to updates from GCP and invalidate the cache accordingly.

import (
	"encoding/json"
	"errors"
	"fmt"

	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

var (
	errEntryNotFound = newDirError("download", "", "pathname not found")
)

func (d *dirServer) getDirEntry(parsedPath *path.Parsed) (*upspin.DirEntry, error) {
	if parsedPath.IsRoot() {
		root, err := d.getRoot(parsedPath.User())
		if err != nil {
			return nil, err
		}
		return &root.dirEntry, nil
	}
	return d.getNonRoot(parsedPath.Path())
}

// putDirEntry writes to the backend at the given path a dir entry that could be a root or a regular dir entry.
// If it's a root dir entry, it first attempts to read an expected-to-exist root in order to update it.
func (d *dirServer) putDirEntry(parsedPath *path.Parsed, dirEntry *upspin.DirEntry) error {
	if parsedPath.IsRoot() {
		root, err := d.getRoot(parsedPath.User())
		if err != nil {
			return err
		}
		root.dirEntry = *dirEntry
		return d.putRoot(parsedPath.User(), root)
	}
	return d.putNonRoot(parsedPath.Path(), dirEntry)
}

// getNonRoot returns the dir entry for the given path, possibly going to stable storage to find it.
func (d *dirServer) getNonRoot(path upspin.PathName) (*upspin.DirEntry, error) {
	log.Printf("Looking up dir entry %q", path)

	// Check cache first.
	if dir, ok := d.dirCache.Get(path); ok {
		de := dir.(upspin.DirEntry)
		return &de, nil
	}

	// Not in cache. Is it in the negative cache?
	if _, ok := d.dirNegCache.Get(path); ok {
		// It *is* in the *negative* cache, so we know it's not found.
		return nil, errEntryNotFound
	}
	var savedDirEntry upspin.DirEntry

	// Lock the dir entry
	dirEntryLock := pathLock(path)
	dirEntryLock.Lock()
	defer dirEntryLock.Unlock()

	buf, err := d.getCloudBytes(path)
	if err != nil {
		if err == errEntryNotFound {
			// Add to the negative cache
			d.dirNegCache.Add(path, nil)
		}
		return nil, err
	}
	err = json.Unmarshal(buf, &savedDirEntry)
	if err != nil {
		return nil, newDirError("getmeta", path, fmt.Sprintf("json unmarshal failed retrieving metadata: %v", err))
	}
	d.dirCache.Add(path, savedDirEntry)
	return &savedDirEntry, nil
}

// putNonRoot forcibly writes to stable storage the given dir entry at the canonical path on the
// backend without checking anything but the marshaling.
func (d *dirServer) putNonRoot(path upspin.PathName, dirEntry *upspin.DirEntry) error {
	// TODO(ehg): if using crypto packing here, as we should, how will secrets get to code at service startup?
	// Save on cache.

	// Lock the dir entry
	dirEntryLock := pathLock(path)
	dirEntryLock.Lock()
	defer dirEntryLock.Unlock()

	d.dirCache.Add(path, *dirEntry)
	d.dirNegCache.Remove(path) // remove from the negative cache in case it was there.
	jsonBuf, err := json.Marshal(dirEntry)
	if err != nil {
		// This is really bad. It means we created a DirEntry that does not marshal to JSON.
		errMsg := fmt.Sprintf("internal server error: conversion to json failed: %s", err)
		log.Error.Printf("%s: %s: %+v", errMsg, path, dirEntry)
		return newDirError("putmeta", path, errMsg)
	}
	log.Printf("Storing dir entry at %q", path)
	_, err = d.cloudClient.Put(string(path), jsonBuf)
	return err
}

// isDirEmpty reports whether the directory path is empty.
func (d *dirServer) isDirEmpty(path upspin.PathName) error {
	dirPrefix := string(path) + "/"
	files, err := d.cloudClient.ListDir(dirPrefix)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return errors.New("directory not empty")
	}
	return nil
}

// getCloudBytes fetches the path from the storage backend.
func (d *dirServer) getCloudBytes(path upspin.PathName) ([]byte, error) {
	log.Printf("Downloading DirEntry from GCP: %s", path)
	data, err := d.cloudClient.Download(string(path))
	if err != nil {
		// TODO: differentiate FILE NOT FOUND from other errors.
		return nil, errEntryNotFound
	}
	return data, nil
}

// deletePath deletes the path from the storage backend and if successful also deletes it from all caches.
func (d *dirServer) deletePath(path upspin.PathName) error {
	// Lock the dir entry
	dirEntryLock := pathLock(path)
	dirEntryLock.Lock()
	defer dirEntryLock.Unlock()

	if err := d.cloudClient.Delete(string(path)); err != nil {
		return err
	}
	d.dirCache.Remove(path)
	d.rootCache.Remove(path)
	d.dirNegCache.Add(path, nil) // a deleted entry goes into the negative cache.
	log.Printf("Deleted %s from GCP and caches", path)
	return nil
}
