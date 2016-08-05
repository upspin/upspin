// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle

import (
	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// This file implements caching and block reading and writing.

// Note: package-level caches are more efficient so a user who comes and goes
// does not leave a dirty cache around. On the other hand, it's possible for a
// single large and active user to hog the cache. But since the DirServer
// is expected to server one or a small set of users, this is fine.
// LRU caches are concurrency-safe.
var (
	locCache    = cache.NewLRU(100)  // Caches <storeLocation, byteBlob>.
	negLocCache = cache.NewLRU(1000) // Caches the absence of a loc, <storeLocation, nothing>.

	errInternalInconsistency = errors.E(errors.IO, errors.Str("internal inconsistency"))
)

func (t *tree) getLocation(loc *upspin.Location) ([]byte, error) {
	const Ref = "tree.getReference"
	if data, ok := locCache.Get(loc); ok {
		if dataBytes, ok := data.([]byte); ok {
			return dataBytes, nil
		}
		return nil, errInternalInconsistency
	}
	// Not in cache. Is it known not to be on store either?
	if _, notOnStore := negLocCache.Get(loc); notOnStore {
		return nil, errors.E(Ref, errors.NotExist)
	}
	// We must fetch it from the Store then.
	store, err := bind.StoreServer(t.context, loc.Endpoint)
	if err != nil {
		return nil, err
	}
	data, locs, err := store.Get(loc.Reference)
	if err != nil {
		if e, ok := err.(*errors.Error); ok {
			if e.Kind == errors.NotExist {
				// Add to negative cache.
				negLocCache.Add(loc, true)
			}
		} else {
			// This is likely an omission. Warn.
			log.Error.Printf("Error from Store not type errors.Error: %q", err)
		}
		return nil, err
	}
	if data != nil {
		locCache.Add(loc, data)
		negLocCache.Remove(loc)
		return data, nil
	}
	if len(locs) > 0 {
		// TODO: this only does one redirection. It also might recurse forever if the redirections refer to each other.
		return t.getLocation(&locs[0])
	}
	// No error, no data, no indirection?
	return nil, errInternalInconsistency
}

// addChildren unmarshals a block of dirEntries corresponding to the contents of a dirEntry
// into a node.
func (t *tree) addChildren(n *node, block []byte) error {
	if n.children == nil {
		n.children = make(map[string]*node)
	}
	if n.dirty {
		log.Error.Printf("addChildren: trying to load a block from storage when the node is dirty.")
		return errInternalInconsistency
	}
	if n.dirEntry == nil || n.dirEntry.Name == "" {
		// Node is fubar.
		return errInternalInconsistency
	}
	var dirs []upspin.DirEntry
	for len(block) > 0 {
		var dir upspin.DirEntry
		remaining, err := dir.Unmarshal(block)
		if err != nil {
			return err
		}
		dirs = append(dirs, dir)
		block = remaining
	}
	// Load children for this node.
	dePath, err := path.Parse(n.dirEntry.Name)
	if err != nil {
		return err
	}
	elemPos := dePath.NElem() + 1
	for _, dir := range dirs {
		p, err := path.Parse(dir.Name)
		if err != nil {
			return err
		}
		if p.NElem() < elemPos {
			// We should never have written a dirEntry whose path does not contain
			// one more element than the parent.
			return errInternalInconsistency
		}
		elem := p.Elem(elemPos)
		if _, exists := n.children[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			return errInternalInconsistency
		}
		n.children[elem] = &node{
			dirEntry: &dir,
		}
	}
	return nil
}

// readDirEntry retrieves all the data for the dir entry.
func (t *tree) readDirEntry(de *upspin.DirEntry) ([]byte, error) {
	packer := pack.Lookup(de.Packing)
	if packer == nil {
		return nil, errors.Errorf("no packing %#x registered", de.Packing)
	}
	u, err := packer.Unpack(t.context, de)
	if err != nil {
		return nil, err
	}
	var data []byte
	for {
		block, ok := u.NextBlock()
		if !ok {
			break
		}
		ciphertext, err := t.getLocation(&block.Location)
		if err != nil {
			return nil, err
		}
		cleartext, err := u.Unpack(ciphertext)
		if err != nil {
			return nil, err
		}
		data = append(data, cleartext...)
	}
	return data, nil
}
