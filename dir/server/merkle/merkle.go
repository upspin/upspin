// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package merkle implements a Merkle tree whose nodes are DirEntry entries.
package merkle

import "upspin.io/upspin"

// Tree is a representation of a directory tree composed of DirEntries.
type Tree interface {
	// Root returns the location of the root. It will be empty if the tree is empty.
	Root() upspin.Location

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

	// Close commits all updated but not yet committed (dirty) entries to their
	// permanent locations and closes the tree. Further uses of the tree will
	// have unpredictable results.
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

	// LastIndex returns the index of the most-recently-appended entry.
	LastIndex() int

	// Drop deletes the entries up to (but not including) the index.
	Drop(index int)

	// Root returns the location of the user's root.
	Root() upspin.Location

	// SetRoot sets the user's root.
	SetRoot(upspin.Location)
}

// Config configures the behavior of the Tree.
type Config struct {
	// StoreEndpoint is where the Tree stores its blocks permanently.
	StoreEndpoint upspin.Endpoint

	// Factotum is the Tree's private key, for authenticating with the Store.
	Factotum upspin.Factotum

	// ServerName is the Tree's user name for authenticating with the Store.
	ServerName upspin.UserName

	// Log manipulates the log on behalf of the tree.
	Log Log
}

// Load loads a tree from its root's location.
func Load(root upspin.Location, config *Config) Tree {
	// TODO
	return nil
}

// NewLog creates a new log or opens an existing log according to the params... To-be-continued.
func NewLog( /* TBD ... */ ) Log {
	// TODO
	return nil
}

// OpenLogs opens all existing logs found according to the params... To-be-continued.
func OpenLogs( /* TBD ... */ ) []Log {
	return nil
}
