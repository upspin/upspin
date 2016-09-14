// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements block reading and writing.

import (
	"strings"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// TODO: move to package upspin or somewhere else more appropriate.
const blockSize = 1024 * 1024 // 1MB

// store stores a node to the StoreServer. It does not reset the dirty bit.
// Children of n, if any, must not be dirty.
func (t *Tree) store(n *node) error {

	// Get our store server.
	storeServer, err := bind.StoreServer(t.context, t.context.StoreEndpoint())
	if err != nil {
		return err
	}

	// Use our preferred packing
	packer := t.packer

	// Can't pack a non-dir entry. Something went bad if we got here.
	if !n.entry.IsDir() {
		err := errors.E(errors.Internal, errors.Str("can't pack non-dir entry"))
		log.Error.Print(err)
		return err
	}

	// Prepare the dirEntry
	n.entry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	n.entry.Packing = t.context.Packing()
	n.entry.Sequence++

	// Start packing.
	bp, err := packer.Pack(t.context, &n.entry)
	if err != nil {
		return errors.E(err)
	}

	// Pack and store child nodes, keeping blocks at ~blockSize.
	var data []byte
	for _, kid := range n.kids {
		// TODO: also check whether there are any Blocks with empty locations that are non-empty dirs or files.
		if kid.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(kid.entry.Name, errors.Internal, errors.Str("kid node is dirty"))
		}
		block, err := kid.entry.Marshal()
		if err != nil {
			return errors.E(err)
		}
		log.Debug.Printf("Tree.store: %s: Saving child: %s. Size: %d", n.entry.Name, kid.entry.Name, len(block))

		// Don't let blocks grow too much (but we never split a large DirEntry in the middle).
		if len(data) > 0 && len(data)+len(block) > blockSize {
			// Flush now.
			err = storeBlock(storeServer, bp, data)
			if err != nil {
				return errors.E(err)
			}
			data = data[:0]
		}
		data = append(data, block...)
	}
	// Put remaining data.
	if len(data) > 0 {
		err = storeBlock(storeServer, bp, data)
		if err != nil {
			return errors.E(err)
		}
	}
	err = bp.Close()
	if err != nil {
		return errors.E(err)
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
func (t *Tree) loadKidsFromBlock(n *node, block []byte) error {
	if n.kids == nil {
		n.kids = make(map[string]*node)
	}
	entryPath, err := path.Parse(n.entry.Name)
	if err != nil {
		return errors.E(err)
	}
	if len(n.kids) > 0 {
		// This means we're trying to load an existing DirEntry onto
		// a directory that has content already. To allow it, we would
		// need to check for name collisions. Disallow for now.
		err := errors.E(errors.Invalid, entryPath.Path(), errors.Str("cannot hide existing contents of path with new block"))
		log.Error.Printf("loadKidsFromBlock: %s", err)
		return err
	}
	if n.entry.Name == "" {
		err := errors.E(errors.Internal, errors.Str("empty entry name"))
		log.Error.Print(err)
		return err
	}
	var dirs []upspin.DirEntry
	for len(block) > 0 {
		var dir upspin.DirEntry
		remaining, err := dir.Unmarshal(block)
		if err != nil {
			return errors.E(err)
		}
		dirs = append(dirs, dir)
		block = remaining
	}
	// Load children for this node.
	elemPos := entryPath.NElem()
	for _, dir := range dirs {
		p, err := path.Parse(dir.Name)
		if err != nil {
			return errors.E(err)
		}
		// elem is the next pathwise element to load. Normally, it's the
		// next element in entryPath. But if it's a directory that
		// doesn't conform with the parent name, we allow it, to support
		// snapshots and other types of "redirected" directories.
		var elem string
		if strings.HasPrefix(p.String(), entryPath.String()) {
			elem = p.Elem(elemPos)
		} else {
			// If the block being loaded is not a prefix of the parent, then
			// it's a "redirection" such as a snapshot entry.
			elem = p.Elem(0)
			log.Printf("== loading redirected elem: %q", elem)
		}
		if _, exists := n.kids[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			err := errors.E(errors.Internal, n.entry.Name,
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
