// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package merkle implements a Merkle tree whose nodes are DirEntry entries.
package merkle

import (
	"sync"
	"time"

	"upspin.io/upspin"
)

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
}

// Config configures the behavior of the Tree.
type Config struct {
	// Store is where the Tree stores its blocks permanently.
	Store upspin.StoreServer

	// Log manipulates the log on behalf of the tree.
	Log Log
}

// New creates an empty Tree for a user.
func New(user upspin.UserName, config *Config) Tree {
	// TODO
	return nil
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

// TODO: the rest of this file should live in another file or in an internal package.
// It's here for now just for the initial check-in and then it will be moved, to keep this clean.

// operation specifies the operation that caused a log entry to be appended.
type operation int

const (
	add operation = iota
	remove
)

// logEntry represents an entry in the log.
type logEntry struct {
	dirEntry upspin.DirEntry
	op       operation
	mTime    time.Time
}

// node is an internal representation of a node in the tree.
type node struct {
	// mu protects all fields.
	mu sync.Mutex

	// dirEntry is the DirEntry this node represents.
	dirEntry *upspin.DirEntry

	// children maps a fragment of a path name to the dir entries that represent them.
	// It is empty if this node's dirEntry represents a file; if it represents a directory,
	// children holds the memory-loaded subdir nodes (not all subdir nodes may be memory-loaded
	// at a given time).
	children map[string]*node

	// dirty indicates whether this node's DirEntry has been modified.
	dirty bool
}

// tree implements Tree.
type tree struct {
	user upspin.UserName
	root *node
	// TBD
}

// get implements the main logic behind Tree.Peek and Tree.Get. It returns the dirEntry if found or an error.
// If the entry is found its dirty status is returned indicating whether its internal references are not
// up-to-date until a flush to permanent storage happens.
func (t *tree) get(path upspin.PathName) (de *upspin.DirEntry, mustFlush bool, err error) {
	return nil, false, nil
}

// TODO
