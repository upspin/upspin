// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package merkle implements a Merkle tree whose nodes are DirEntry entries.
package merkle

import (
	"sync"

	"upspin.io/upspin"
)

// Tree is a representation of a directory tree composed of DirEntries.
type Tree interface {
	// RootLoc returns the location of the root. It may be empty if the tree is empty.
	RootLoc() upspin.Location

	// Load loads a path and all its ancestors from disk to memory and returns the
	// DirEntry that represents the path. If any portion of the path is already in memory
	// no reads from disk happen.
	Load(path upspin.PathName) (*upspin.DirEntry, error)

	// Add adds a DirEntry de as a child of another DirEntry.
	// If de overwrites a file in childOf, it is okay, but if it overwrites a directory
	// an error will be returned.
	// This is primarily intended to support DirServer.Put and DirServer.MakeDirectory.
	Add(de *upspin.DirEntry, childOf *upspin.DirEntry) error

	// Remove removes the DirEntry associated with name. As a side-effect, it loads
	// any path segment of name that is not already in memory.
	// This is primarily intended to support DirServer.Delete.
	Remove(name upspin.PathName) error

	// Flush commits all updated but not yet committed (dirty) entries to their
	// permanent locations. It does not affect the in-memory tree.
	Flush() error

	// TODO: possibly add Trim(path) so we can remove internal nodes from memory,
	// recursively from a starting path. For now our assumption is that the tree will always
	// fit in memory.
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

	// dirty indicates whether this node has been modified.
	dirty bool
}

// Loader loads a byte-slice blob that represents a block of DirEntries in marshalled format.
type Loader func(loc upspin.Location) ([]byte, error)

// Saver saves a blob to an endpoint and returns its location.
type Saver func(blob []byte, e upspin.Endpoint) (upspin.Location, error)

// New creates an empty Tree for a user.
func New(user upspin.UserName, loader Loader, saver Saver) Tree {
	// TODO
	return nil
}

// Load loads a tree from its root's location.
func Load(root upspin.Location, loader Loader, saver Saver) Tree {
	// TODO
	return nil
}

// TODO
