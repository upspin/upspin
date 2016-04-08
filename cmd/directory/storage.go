package main

// This file deals with low-level storage of dir entries on GCP, their mashaling and unmashaling and caching.

// TODO: we only have one server, so we know when the cache is invalid which is never, since we update it every time
// we update the backend. But this won't be true if we ever have more than one server serving a given user. At that point,
// we need to subscribe to updates from GCP and invalidate the cache accordingly.

import (
	"encoding/json"
	"fmt"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cache"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	// dirCache caches <path, annotatedDirEntry>
	dirCache = cache.NewLRU(1000)
)

// annotatedDirEntry is an upspin.DirEntry with annotated with extra information.
type annotatedDirEntry struct {
	// d is the upspin.DirEntry we return to clients.
	d upspin.DirEntry

	// access contains the parsed contents of an Access file if d represents a directory and
	// an Access file exists in the directory tree that rules over d.
	access *access.Access
}

// savedDirEntry is a representation of annotatedDirEntry that only exists on disk
// and is never visible outside of getMeta or putMeta.
type savedDirEntry struct {
	DirEntry *upspin.DirEntry
	Access   json.RawMessage
}

// getMeta returns the metadata for the given path, possibly going to stable storage to find it.
func (d *dirServer) getMeta(path upspin.PathName) (*annotatedDirEntry, error) {
	logMsg.Printf("Looking up dir entry %q on storage backend", path)

	// Check cache first
	if adir, ok := dirCache.Get(path); ok {
		return adir.(*annotatedDirEntry).access, nil
	}

	// Not in cache.
	var savedDirEntry savedDirEntry
	buf, err := d.getCloudBytes(path)
	if err != nil {
		return &savedDirEntry, err
	}
	err = json.Unmarshal(buf, &savedDirEntry)
	if err != nil {
		return &savedDirEntry, newDirError("getmeta", path, fmt.Sprintf("json unmarshal failed retrieving metadata: %v", err))
	}
	adir := &annotatedDirEntry{
		d:      savedDirEntry.DirEntry,
		access: nil, // may be parsed below.
	}
	if savedDirEntry.Access != nil {
		adir.access, err = access.UnmarshalJSON(path, savedDirEntry.Access)
		if err != nil {
			return nil, newDirError("getmeta", path, fmt.Sprintf("json unmarshal access failed: %s", err))
		}
	}
	dirCache.Add(path, adir)
	return adir, nil
}

// putMeta forcibly writes to stable storage the given annotatedDirEntry at the canonical path on the
// backend without checking anything but the marshaling.
func (d *dirServer) putMeta(path upspin.PathName, aDirEntry *annotatedDirEntry) error {
	// TODO(ehg): if using crypto packing here, as we should, how will secrets get to code at service startup?
	s := savedDirEntry{
		DirEntry: aDirEntry.d,
	}

	jsonBuf, err := json.Marshal(s)
	if err != nil {
		// This is really bad. It means we created a DirEntry that does not marshal to JSON.
		errMsg := fmt.Sprintf("internal server error: conversion to json failed: %s", err)
		logErr.Printf("WARN: %s: %s: %+v", errMsg, path, s)
		return newDirError("putmeta", path, errMsg)
	}
	if aDirEntry.access != nil {
		savedDirEntry.Access, err = aDirEntry.access.MarshalJSON()
		if err != nil {
			errMsg := fmt.Sprintf("internal server error: access conversion to json failed: %s", err)
			logErr.Printf("WARN: %s: %s: %+v", errMsg, path, aDirEntry)
			return newDirError("putmeta", path, errMsg)
		}
	}
	logMsg.Printf("Storing dir entry at %q", path)
	_, err = d.cloudClient.Put(string(path), jsonBuf)
	dirCache.Add(path, aDirEntry)
	return err
}

// getAccessFor returns the parsed contents of the Access file that rules over a path. It may be nil.
// This method uses caching, but it may perform at most one lookup on disk.
func (d *dirServer) getAccessFor(path upspin.PathName) (*access.Access, error) {
	adir, err := d.getMeta(path)
	if err != nil {
		return nil, err
	}
	return adir.access
}

// getCloudBytes fetches the path from the storage backend.
func (d *dirServer) getCloudBytes(path upspin.PathName) ([]byte, error) {
	data, err := d.cloudClient.Download(string(path))
	if err != nil {
		return nil, errEntryNotFound
	}
	return data, err
}
