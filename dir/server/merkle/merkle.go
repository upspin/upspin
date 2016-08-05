// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package merkle implements a Merkle tree whose nodes are DirEntry entries.
package merkle

import "upspin.io/upspin"

// Tree is a representation of a directory tree composed of DirEntries.
type Tree interface {
	// Root returns the root. Its blocks will be empty if the tree is empty.
	Root() *upspin.DirEntry

	// Lookup returns a DirEntry (de) that represents the path. The returned de may or may not
	// have valid references inside. If dirty is true, the references are not up-to-date.
	// Call Flush first to get an updated DirEntry.
	Lookup(path upspin.PathName) (de *upspin.DirEntry, dirty bool, err error)

	// Put puts a DirEntry de. If de overwrites a file, it is okay, but if it overwrites
	// a directory an error will be returned.
	Put(de *upspin.DirEntry) error

	// Delete deletes the DirEntry associated with name.
	Delete(name upspin.PathName) error

	// Flush flushes all dirty entries.
	Flush() error

	// Close flushes all dirty blocks to Store and releases all resources used by Tree.
	// Further uses of the tree will have unpredictable results.
	Close() error

	// TODO: possibly add Trim(path) so we can remove internal nodes from memory,
	// recursively from a starting path. For now our assumption is that the tree will always
	// fit in memory.
}

// Log represents the log of DirEntry changes. It is primarily used by
// Tree (provided via its Config struct) to log changes.
type Log interface {
	// User returns the user name for whom this log logs.
	User() upspin.UserName

	// Append appends a DirEntry at the end of the log.
	Append(*upspin.DirEntry)

	// Read reads at most n entries from the log starting at index.
	Read(index, n int) []*upspin.DirEntry

	// LastIndex returns the index of the most-recently-appended entry or -1 if log is empty.
	LastIndex() int

	// Drop deletes the entries up to the index.
	Drop(index int)

	// Root returns the user's root.
	Root() *upspin.DirEntry

	// SetRoot sets the user's root.
	SetRoot(*upspin.DirEntry)
}

// Config configures the behavior of the Tree.
type Config struct {
	// Context is a server context. It is used for contacting StoreServer,
	// defining the default packing and setting the server name. The only
	// field in Context that is not needed directly or indirectly is the
	// directory endpoint. Everything else is required.
	Context upspin.Context

	// Log manipulates the log on behalf of the tree.
	Log Log
}
