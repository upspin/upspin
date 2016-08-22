// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements block reading and writing.

import (
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// TODO: move to package upspin or somewhere else more appropriate.
const blockSize = 1024 * 1024 // 1MB

// store stores a node to the StoreServer. It does not reset the dirty bit.
// Children of n, if any, must not be dirty.
func (t *tree) store(n *node) error {
	const op = "tree.store"

	// Get our store server.
	storeServer := t.context.StoreServer()

	// Use our preferred packing
	packer := t.packer

	// Can't pack a non-dir entry. Something went bad if we got here.
	if !n.entry.IsDir() {
		err := errors.E(op, errors.Internal, errors.Str("can't pack non-dir entry"))
		log.Error.Print(err)
		return err
	}

	// Prepare the dirEntry
	n.entry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	n.entry.Packing = t.context.Packing()

	// Start packing.
	bp, err := packer.Pack(t.context, &n.entry)
	if err != nil {
		return errors.E(op, err)
	}

	// Pack and store child nodes, keeping blocks at ~blockSize.
	var data []byte
	for _, kid := range n.kids {
		// TODO: also check whether there are any Blocks with empty locations that are non-empty dirs or files.
		if kid.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(op, kid.entry.Name, errors.Internal, errors.Str("kid node is dirty"))
		}
		block, err := kid.entry.Marshal()
		if err != nil {
			return errors.E(op, err)
		}
		log.Debug.Printf("%s: %s: Saving child: %s. Size: %d", op, n.entry.Name, kid.entry.Name, len(block))

		// Don't let blocks grow too much (but we never split a large DirEntry in the middle).
		if len(data) > 0 && len(data)+len(block) > blockSize {
			// Flush now.
			err = storeBlock(storeServer, bp, data)
			if err != nil {
				return errors.E(op, err)
			}
			data = data[:0]
		}
		data = append(data, block...)
	}
	// Put remaining data.
	if len(data) > 0 {
		err = storeBlock(storeServer, bp, data)
		if err != nil {
			return errors.E(op, err)
		}
	}
	err = bp.Close()
	if err != nil {
		return errors.E(op, err)
	}
	return nil
}

// storeBlock stores a single block of data to the StoreServer as part of a block packing operation.
func storeBlock(store upspin.StoreServer, bp upspin.BlockPacker, data []byte) error {
	// TODO(edpin): remove logging once debugging is done.
	log.Debug.Printf("Writing %d bytes of data", len(data))
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

// loadKidsFromBlock unmarshals a block of packed dirEntries into a node.
func (t *tree) loadKidsFromBlock(n *node, block []byte) error {
	const loadKidsFromBlock = "loadKidsFromBlock"
	if n.kids == nil {
		n.kids = make(map[string]*node)
	}
	if n.dirty {
		err := errors.E(loadKidsFromBlock, errors.Internal, n.entry.Name,
			errors.Str("trying to load a block from storage when the node is dirty"))
		log.Error.Print(err)
		return err
	}
	if n.entry.Name == "" {
		err := errors.E(loadKidsFromBlock, errors.Internal, errors.Str("empty entry name"))
		log.Error.Print(err)
		return err
	}
	var dirs []upspin.DirEntry
	for len(block) > 0 {
		var dir upspin.DirEntry
		remaining, err := dir.Unmarshal(block)
		if err != nil {
			return errors.E(loadKidsFromBlock, err)
		}
		dirs = append(dirs, dir)
		block = remaining
	}
	// Load children for this node.
	dePath, err := path.Parse(n.entry.Name)
	if err != nil {
		return errors.E(loadKidsFromBlock, err)
	}
	elemPos := dePath.NElem()
	for _, dir := range dirs {
		p, err := path.Parse(dir.Name)
		if err != nil {
			return errors.E(loadKidsFromBlock, err)
		}
		if p.NElem() <= elemPos {
			// We should never have written a dirEntry whose path does not contain
			// one more element than the parent.
			err := errors.E(loadKidsFromBlock, errors.Internal, n.entry.Name,
				errors.Str("entry is inconsistent with parent"))
			log.Error.Print(err)
			return err
		}
		elem := p.Elem(elemPos)
		if _, exists := n.kids[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			err := errors.E(loadKidsFromBlock, errors.Internal, n.entry.Name,
				errors.Str("re-adding an existing element in the Tree"))
			log.Error.Print(err)
			return err
		}
		n.kids[elem] = &node{
			entry: dir,
		}
	}
	return nil
}
