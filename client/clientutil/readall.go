// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package clientutil implements common utilities shared by clients and those
// who act as clients, such as a DirServer being a client of a StoreServer.
package clientutil

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/upspin"
	"upspin.io/valid"
)

// ReadAll reads the entire contents of a DirEntry. The reader must have
// the necessary keys loaded in the context to unpack the cipher if the entry
// is encrypted.
func ReadAll(ctx upspin.Context, entry *upspin.DirEntry) ([]byte, error) {
	const op = "clientutil.ReadAll"

	// Validate the entry and its blocks.
	if err := valid.DirEntry(entry); err != nil {
		return nil, errors.E(op, err)
	}
	if entry.IsLink() {
		return nil, errors.E(op, errors.Invalid, errors.Str("can't read a link entry"))
	}

	// firstError remembers the first error we saw. If we fail completely we return it.
	var firstError error
	// isError reports whether err is non-nil and remembers it if it is.
	isError := func(err error) bool {
		if err == nil {
			return false
		}
		if firstError == nil {
			firstError = err
		}
		return true
	}

	var data []byte
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.E(op, entry.Name, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(ctx, entry)
	if err != nil {
		return nil, errors.E(op, entry.Name, err) // Showstopper.
	}
Blocks:
	for b := 0; ; b++ {
		block, ok := bu.NextBlock()
		if !ok {
			break // EOF
		}
		// block is known valid as per valid.DirEntry above.

		// knownLocs stores the known Locations for this block. Value is
		// ignored.
		knownLocs := make(map[upspin.Location]bool)
		// Get the data for this block.
		// where is the list of locations to examine. It is updated in the loop.
		where := []upspin.Location{block.Location}
		for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
			loc := where[i]
			store, err := bind.StoreServer(ctx, loc.Endpoint)
			if isError(err) {
				continue
			}
			cipher, locs, err := store.Get(loc.Reference)
			if isError(err) {
				continue // locs guaranteed to be nil.
			}
			if locs == nil && err == nil {
				// Found the data. Unpack it.
				clear, err := bu.Unpack(cipher)
				if err != nil {
					return nil, errors.E(op, entry.Name, err) // Showstopper.
				}
				data = append(data, clear...) // TODO: Could avoid a copy if only one block.
				continue Blocks
			}
			// Add new locs to the list. Skip ones already there - they've been processed.
			for _, newLoc := range locs {
				if _, found := knownLocs[newLoc]; !found {
					where = append(where, newLoc)
					knownLocs[newLoc] = true
				}
			}
		}
		// If we arrive here, we have failed to find a block.
		if firstError != nil {
			return nil, errors.E(op, entry.Name, firstError)
		}
		return nil, errors.E(op, entry.Name, errors.IO,
			errors.Errorf("data for block %d not found on any store server", b))
	}
	return data, nil
}
