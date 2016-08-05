// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle

// This file implements block reading and writing.

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

const blockSize = 1024 * 1024 // 1MB

// readLocation reads the contents of a location as a blob.
func (t *tree) readLocation(loc *upspin.Location) ([]byte, error) {
	const readLocation = "readLocation"
	store, err := bind.StoreServer(t.context, loc.Endpoint)
	if err != nil {
		return nil, err
	}
	data, locs, err := store.Get(loc.Reference)
	if err != nil {
		return nil, errors.E(readLocation, err)
	}
	if data != nil {
		return data, nil
	}
	if len(locs) > 0 {
		// TODO: this only does one redirection. It also might recurse forever if the redirections refer to each other.
		return t.readLocation(&locs[0])
	}
	// No error, no data, no indirection?
	return nil, errors.E(readLocation, errInternalInconsistency)
}

// writeNode writes a node to the StoreServer. It does not reset the dirty bit.
// Children of n, if any, must not be dirty.
func (t *tree) writeNode(n *node) error {
	const writeNode = "writeNode"

	// Get our store server.
	store := t.context.StoreServer()

	// Get our preferred packing
	packer := pack.Lookup(t.context.Packing())
	if packer == nil {
		return errors.Errorf("no packing %s registered", t.context.Packing())
	}

	// Prepare the dirEntry
	n.dirEntry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	n.dirEntry.Packing = t.context.Packing()

	// Start packing.
	bp, err := packer.Pack(t.context, n.dirEntry)
	if err != nil {
		return errors.E(writeNode, err)
	}

	// Pack as many children nodes as we can fit in a block size.
	var data []byte
	for _, child := range n.children {
		// TODO: also check whether there are any Blocks with empty locations that are non-empty dirs or files.
		if child.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(writeNode, errInternalInconsistency)
		}
		log.Debug.Printf("%s: Saving child: %s", n.dirEntry.Name, child.dirEntry.Name)
		block, err := child.dirEntry.Marshal()
		if err != nil {
			return errors.E(writeNode, err)
		}
		// Append to data.
		data = append(data, block...)

		// Don't let blocks grow too much (but we never split a large DirEntry in the middle).
		if len(data) >= blockSize {
			// Flush now.
			err = t.writeBlockInternal(data, bp, store)
			if err != nil {
				return errors.E(writeNode, err)
			}
			data = data[0:0]
		}
	}
	// Put remaining data.
	if len(data) > 0 {
		err = t.writeBlockInternal(data, bp, store)
		if err != nil {
			return errors.E(writeNode, err)
		}
	}
	err = bp.Close()
	if err != nil {
		return errors.E(writeNode, err)
	}
	return nil
}

// writeBlockInternal is a helper method to put a single block of data to the StoreServer as part
// of an on-going block packing operation.
func (t *tree) writeBlockInternal(data []byte, bp upspin.BlockPacker, store upspin.StoreServer) error {
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
	elemPos := dePath.NElem()
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
		ciphertext, err := t.readLocation(&block.Location)
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
