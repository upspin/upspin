// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree // import "upspin.io/dir/server/tree"

// This file implements block reading and writing.

import (
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// store marshals kids and packs them for the given DirEntry, updating its
// Packdata and Blocks, and stores them to the StoreServer.
func (t *Tree) store(entry *upspin.DirEntry, kids map[string]*node) error {
	// Get our store server.
	storeServer, err := bind.StoreServer(t.config, t.config.StoreEndpoint())
	if err != nil {
		return err
	}

	// Use our preferred packing
	packer := t.packer

	// Can't pack a non-dir entry. Something went bad if we got here.
	if !entry.IsDir() {
		err := errors.E(errors.Internal, "can't pack non-dir entry")
		return err
	}

	// Prepare the dirEntry
	entry.Blocks = nil // if any blocks existed, their references are lost as we're packing dirEntry again.
	entry.Packing = t.config.Packing()
	entry.Time = upspin.Now()
	entry.Writer = t.config.UserName()
	// Sequence number is already up-to-date (see setNodeDirtyAt).

	// Start packing.
	bp, err := packer.Pack(t.config, entry)
	if err != nil {
		return err
	}

	// Pack and store child nodes, keeping blocks at ~BlockSize.
	var data []byte
	for _, kid := range kids {
		if kid.dirty {
			// We should write nodes from the bottom up, so this should never happen.
			return errors.E(kid.entry.Name, errors.Internal, "kid node is dirty")
		}
		// Check whether there are any empty Blocks or locations that
		// are non-empty dirs or files.
		if len(kid.kids) != 0 {
			for _, b := range kid.entry.Blocks {
				if len(b.Location.Reference) == 0 || b.Size == 0 {
					return errors.E(kid.entry.Name, errors.Internal, "empty directory block when there exist kid blocks")
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
	return bp.Close()
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

// Early logs, now called version 0 logs, used a sequence number that varied per
// file. Each file's sequence number grew from a unique, random number in the
// high bits, reserving the low 23 bits for an actual sequence. This was to
// prevent certain race conditions, most important an attempt to write a file at
// a sequence number when the file had been deleted and recreated underfoot.
// From version 1 onward, the sequence numbers increment at the tree level, and
// such races are impossible. We therefore don't bother with random high bits
// any more. For consistency, though, we need to clear the high bits when we
// have sequence numbers recovered from version 0 logs. This constant is used in
// the loading of the DirEntry in loadKidsFromBlock to clear those bits.
const version0SeqMask = 1<<23 - 1

// load fetches the blocks for a DirEntry from the StoreServer and unmarshals
// them into a map of kids.
func (t *Tree) load(entry *upspin.DirEntry) (kids map[string]*node, err error) {
	nodePath, err := path.Parse(entry.Name)
	if err != nil {
		return nil, err
	}
	data, err := clientutil.ReadAll(t.config, entry)
	if err != nil {
		return nil, err
	}
	// Load children for this node.
	kids = make(map[string]*node)
	elemPos := nodePath.NElem()
	v1Transition := t.user.V1Transition()
	for len(data) > 0 {
		var kid upspin.DirEntry
		remaining, err := kid.Unmarshal(data)
		if err != nil {
			return nil, err
		}
		data = remaining

		// Process this entry.
		p, err := path.Parse(kid.Name)
		if err != nil {
			return nil, err
		}
		// Is this an old entry? If so, clear the high bits of the sequence number.
		if kid.Time < v1Transition {
			kid.Sequence &= version0SeqMask
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
			kid.Name = path.Join(nodePath.Path(), elem)
		}
		if _, exists := kids[elem]; exists {
			// Trying to re-add an existing child. Something is amiss.
			return nil, errors.E(errors.Internal, kid.Name, "re-adding an existing element in the Tree")
		}
		kids[elem] = &node{entry: kid}
	}
	return kids, nil
}
