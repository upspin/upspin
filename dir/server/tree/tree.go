// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tree implements a tree whose nodes are DirEntry entries.
package tree

import (
	"fmt"

	"upspin.io/path"
	"upspin.io/upspin"
)

// Tree is a representation of a directory tree for a single Upspin user.
// The tree reads and writes from/to its backing Store server, which is
// configured when instantiating the Tree. It uses a Log to log changes not
// yet committed to the Store.
// In all methods returning directory entries, the entries are of  pointer type
// for performance reasons and modifying them is not allowed -- make a copy
// first if necessary.
// TODO: delete the interface.
// TODO: change all paths from upspin.PathName to path.Parsed. (Issue #35)
type Tree interface {
	// Root returns the root. Its blocks will be empty if the tree is empty.
	Root() (*upspin.DirEntry, error)

	// Lookup returns an entry that represents the path. The returned
	// DirEntry may or may not have valid references inside. If dirty is
	// true, the references are not up-to-date. Calling Flush in a critical
	// section prior to Lookup will ensure the entry is not dirty.
	//
	// If the returned error is ErrFollowLink, the caller should retry the
	// operation as outlined in the description for upspin.ErrFollowLink.
	// Otherwise in the case of error the returned DirEntry will be nil.
	Lookup(path upspin.PathName) (de *upspin.DirEntry, dirty bool, err error)

	// Put puts an entry into the Tree. If the entry exists, it will be
	// overwritten.
	//
	// If the returned error is ErrFollowLink, the caller should retry the
	// operation as outlined in the description for upspin.ErrFollowLink
	// (with the added step of updating the Name field of the argument
	// DirEntry). Otherwise, the returned DirEntry will be nil whether the
	// operation succeeded or not.
	Put(de *upspin.DirEntry) (*upspin.DirEntry, error)

	// Delete deletes the entry associated with name. If the name identifies
	// a link, Delete will delete the link itself, not its target.
	//
	// If the returned error is upspin.ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// upspin.ErrFollowLink. (And in that case, the DirEntry will never
	// represent the full path name of the argument.) Otherwise, the
	// returned DirEntry will be nil whether the operation succeeded
	// or not.
	Delete(name upspin.PathName) (*upspin.DirEntry, error)

	// List lists the contents of a prefix. If prefix names a directory, all
	// entries of the directory are returned. If prefix names a file, that
	// file's entry is returned. List does not interpret wildcards. Dirty
	// reports whether any DirEntry returned is dirty (and thus may contain
	// outdated references).
	//
	// If the returned error is upspin.ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// upspin.ErrFollowLink. (And in that case, only one DirEntry will be
	// returned, that of the link itself.)
	List(prefix path.Parsed) (entries []*upspin.DirEntry, dirty bool, err error)

	// Flush flushes all dirty dir entries to the Tree's Store.
	Flush() error

	// Close flushes all dirty blocks to Store and releases all resources
	// used by the tree. Further uses of the tree will have unpredictable
	// results.
	Close() error

	// For printing the Tree.
	fmt.Stringer

	// TODO: possibly add Trim(path) so we can remove internal nodes from memory,
	// recursively from a starting path. For now our assumption is that the tree will always
	// fit in memory.
}

// Operation is the kind of operation performed on the DirEntry.
type Operation int

// Operations on dir entries that are logged.
const (
	Put Operation = iota
	Delete
)

// LogEntry is the unit of logging.
type LogEntry struct {
	Op    Operation
	Entry upspin.DirEntry
}

// Log represents the log of DirEntry changes. It is primarily used by
// Tree (provided through its Config struct) to log changes.
type Log interface {
	// User returns the user name who owns the root of the tree that this log represents.
	User() upspin.UserName

	// Append appends a LogEntry to the end of the log.
	Append(*LogEntry) error

	// ReadAt reads at most n entries from the log starting at offset. It
	// returns the next offset.
	ReadAt(n int, offset int64) ([]LogEntry, int64, error)

	// LastOffset returns the offset of the most-recently-appended entry or 0 if log is empty.
	LastOffset() int64
}

// LogIndex reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress processing
// the log and storing the root.
type LogIndex interface {
	// User returns the user name who owns the root of the tree that this
	// log index represents.
	User() upspin.UserName

	// Root returns the user's root by retrieving it from local stable storage.
	Root() (*upspin.DirEntry, error)

	// SaveRoot saves the user's root entry to stable storage.
	SaveRoot(*upspin.DirEntry) error

	// ReadOffset reads from stable storage the offset saved by SaveOffset.
	ReadOffset() (int64, error)

	// SaveOffset saves to stable storage the offset to process next.
	SaveOffset(int64) error
}
