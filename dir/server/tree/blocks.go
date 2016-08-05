// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements block reading and writing.

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// TODO: move to package upspin or somewhere else more appropriate.
const blockSize = 1024 * 1024 // 1MB

// getLocation gets the contents of a location as a blob.
func (t *tree) get(loc *upspin.Location) ([]byte, error) {
	const get = "get"
	store, err := bind.StoreServer(t.context, loc.Endpoint)
	if err != nil {
		return nil, err
	}
	data, locs, err := store.Get(loc.Reference)
	if err != nil {
		return nil, errors.E(get, err)
	}
	if data != nil && len(locs) > 0 {
		return nil, errors.E(get, errInternalInconsistency)
	}
	if data != nil {
		return data, nil
	}
	// TODO: this should do something akin to client.Get. It now only does one indirection.
	if len(locs) > 0 {
		return t.get(&locs[0])
	}
	// No error, no data, no indirection?
	return nil, errors.E(get, errInternalInconsistency)
}

// store stores a node to the StoreServer. It does not reset the dirty bit.
// Children of n, if any, must not be dirty.
func (t *tree) store(n *node) error {
	const store = "writeNode"

	// Get our store server.
	storeServer := t.context.StoreServer()

	// Use our preferred packing
	var packer upspin.Packer
	if t.packer != nil {
		packer = t.packer
	} else {
		packer = pack.Lookup(t.context.Packing())
		if packer == nil {
			return errors.E(store, errors.Errorf("no packing %s registered", t.context.Packing()))
		}
	}

	// Prepare the dirEntry
	n.entry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	n.entry.Packing = t.context.Packing()

	// Start packing.
	bp, err := packer.Pack(t.context, n.entry)
	if err != nil {
		return errors.E(store, err)
	}

	// Pack and store child nodes, keeping blocks at ~blockSize.
	var data []byte
	for _, child := range n.kids {
		// TODO: also check whether there are any Blocks with empty locations that are non-empty dirs or files.
		if child.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(store, child.entry.Name, errInternalInconsistency)
		}
		log.Debug.Printf("%s: Saving child: %s", n.entry.Name, child.entry.Name)
		block, err := child.entry.Marshal()
		if err != nil {
			return errors.E(store, err)
		}
		data = append(data, block...)

		// Don't let blocks grow too much (but we never split a large DirEntry in the middle).
		if len(data) >= blockSize {
			// Flush now.
			err = writeBlock(storeServer, bp, data)
			if err != nil {
				return errors.E(store, err)
			}
			data = data[0:0]
		}
	}
	// Put remaining data.
	if len(data) > 0 {
		err = writeBlock(storeServer, bp, data)
		if err != nil {
			return errors.E(store, err)
		}
	}
	err = bp.Close()
	if err != nil {
		return errors.E(store, err)
	}
	return nil
}

// writeBlock writes a single block of data to the StoreServer as part of a block packing operation.
func writeBlock(store upspin.StoreServer, bp upspin.BlockPacker, data []byte) error {
	log.Debug.Print("Writing %d bytes of data", len(data))
	cipher, err := bp.Pack(data)
	if err != nil {
		return err
	}
	ref, err := store.Put(cipher)
	if err != nil {
		return err
	}
	loc := upspin.Location{
		Endpoint:  store.Endpoint(),
		Reference: ref,
	}
	bp.SetLocation(loc)
	return nil
}

// addChildren unmarshals a block of packed dirEntries into a node.
func (t *tree) addChildren(n *node, block []byte) error {
	if n.kids == nil {
		n.kids = make(map[string]*node)
	}
	if n.dirty {
		log.Error.Printf("addChildren: trying to load a block from storage when the node %s is dirty.", n.entry.Name)
		return errInternalInconsistency
	}
	if n.entry == nil || n.entry.Name == "" {
		// Node is fubar. TODO: log details.
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
	dePath, err := path.Parse(n.entry.Name)
	if err != nil {
		return err
	}
	elemPos := dePath.NElem()
	for _, dir := range dirs {
		p, err := path.Parse(dir.Name)
		if err != nil {
			return err
		}
		if p.NElem() <= elemPos {
			// We should never have written a dirEntry whose path does not contain
			// one more element than the parent.
			return errInternalInconsistency
		}
		elem := p.Elem(elemPos)
		if _, exists := n.kids[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			return errInternalInconsistency
		}
		n.kids[elem] = &node{
			entry: &dir,
		}
	}
	return nil
}

// readDirEntry retrieves all the data for a dir entry.
func (t *tree) readDirEntry(de *upspin.DirEntry) ([]byte, error) {
	packer := pack.Lookup(de.Packing)
	if packer == nil {
		return nil, errors.Errorf("no packing %s registered", de.Packing)
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
		ciphertext, err := t.get(&block.Location)
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
