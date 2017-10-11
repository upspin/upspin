// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree // import "upspin.io/dir/server/tree"

// This file implements block reading and writing.

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// store stores a node to the StoreServer. It does not reset the dirty bit.
// Children of n, if any, must not be dirty.
func (t *Tree) store(n *node) error {
	// Get our store server.
	storeServer, err := bind.StoreServer(t.config, t.config.StoreEndpoint())
	if err != nil {
		return err
	}

	// Use our preferred packing
	packer := t.packer

	// Can't pack a non-dir entry. Something went bad if we got here.
	if !n.entry.IsDir() {
		err := errors.E(errors.Internal, errors.Str("can't pack non-dir entry"))
		return err
	}

	// Prepare the dirEntry
	n.entry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	n.entry.Packing = t.config.Packing()
	n.entry.Time = upspin.Now()
	n.entry.Writer = t.config.UserName()
	// Sequence number is already up-to-date (see setNodeDirtyAt).

	// Start packing.
	bp, err := packer.Pack(t.config, &n.entry)
	if err != nil {
		return err
	}

	// Pack and store child nodes, keeping blocks at ~BlockSize.
	var data []byte
	for _, kid := range n.kids {
		if kid.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(kid.entry.Name, errors.Internal, errors.Str("kid node is dirty"))
		}
		// Check whether there are any empty Blocks or locations that
		// are non-empty dirs or files.
		if len(kid.kids) != 0 {
			for _, b := range kid.entry.Blocks {
				if len(b.Location.Reference) == 0 || b.Size == 0 {
					return errors.E(kid.entry.Name, errors.Internal, errors.Str("empty directory block when there exist kid blocks"))
				}
			}
		}
		block, err := kid.entry.Marshal()
		if err != nil {
			return err
		}

		// Don't let blocks grow too much (but we never split a large DirEntry in the middle).
		if len(data) > 0 && len(data)+len(block) > upspin.BlockSize {
			// Flush now.
			err = storeBlock(storeServer, bp, data)
			if err != nil {
				return err
			}
			data = data[:0]
		}
		data = append(data, block...)
	}
	// Put remaining data.
	if len(data) > 0 {
		err = storeBlock(storeServer, bp, data)
		if err != nil {
			return err
		}
	}
	err = bp.Close()
	if err != nil {
		return err
	}
	return nil
}

// storeBlock stores a single block of data to the StoreServer as part of a block packing operation.
func storeBlock(store upspin.StoreServer, bp upspin.BlockPacker, data []byte) error {
	cipher, err := bp.Pack(data)
	if err != nil {
		return err
	}
	refdata, err := store.Put(cipher)
	if err != nil {
		return err
	}
	loc := upspin.Location{
		Endpoint:  store.Endpoint(),
		Reference: refdata.Reference,
	}
	bp.SetLocation(loc)
	return nil
}

const version0SeqMask = 1<<23 - 1

// loadKidsFromBlock unmarshals a block of packed dirEntries into a node.
func (t *Tree) loadKidsFromBlock(n *node, block []byte) error {
	if n.kids == nil {
		n.kids = make(map[string]*node)
	}
	if n.dirty {
		err := errors.E(errors.Internal, n.entry.Name,
			errors.Str("trying to load a block from storage when the node is dirty"))
		return err
	}
	nodePath, err := path.Parse(n.entry.Name)
	if err != nil {
		return err
	}
	if len(n.kids) > 0 {
		// This means we're trying to load an existing DirEntry onto
		// a directory that has content already. To allow it, we would
		// need to check for name collisions. Disallow for now.
		err := errors.E(errors.Invalid, nodePath.Path(), errors.Str("cannot hide existing contents of path with new block"))
		return err
	}
	if n.entry.Name == "" {
		err := errors.E(errors.Internal, errors.Str("empty entry name"))
		return err
	}
	// Load children for this node.
	elemPos := nodePath.NElem()
	v1Transition := t.user.V1Transition()
	for len(block) > 0 {
		var entry upspin.DirEntry
		remaining, err := entry.Unmarshal(block)
		if err != nil {
			return err
		}
		block = remaining

		// Process this entry.
		p, err := path.Parse(entry.Name)
		if err != nil {
			return err
		}
		// Is this an old entry? If so, clear the high bits of the sequence number.
		if entry.Time < v1Transition {
			entry.Sequence &= version0SeqMask
		}
		// elem is the next pathwise element to load. Normally, it's the
		// next element in entryPath. But if it's a directory that
		// doesn't conform with the parent name, we allow it, to support
		// snapshots and other types of "redirected" directories. But we
		// must patch it according to this tree's perspective.
		var elem string
		if p.Drop(1).Equal(nodePath) {
			elem = p.Elem(elemPos)
		} else {
			// If the block being loaded is not a prefix of the
			// parent, then it's a "redirection" such as a snapshot
			// entry.
			elem = p.Elem(p.NElem() - 1) // ok, never root.
			// Patch the entry's Name, so it belongs to this tree.
			entry.Name = path.Join(nodePath.Path(), elem)
		}
		if _, exists := n.kids[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			err := errors.E(errors.Internal, n.entry.Name,
				errors.Str("re-adding an existing element in the Tree"))
			return err
		}
		n.kids[elem] = &node{
			entry: entry,
		}
	}
	return nil
}
