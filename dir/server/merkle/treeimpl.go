// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle

// This file implements the Tree interface declared in merkle.go.

import (
	"sync"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// node is an internal representation of a node in the tree.
type node struct {
	// dirEntry is the DirEntry this node represents.
	dirEntry *upspin.DirEntry

	// children maps a fragment of a path name to the dir entries that represent them.
	// It is empty if this node's dirEntry represents a file; if it represents a directory,
	// children holds the memory-loaded subdir nodes (not all subdir nodes may be memory-loaded
	// at a given time). Map accesses must be protected by a lock.
	children map[string]*node

	// dirty indicates whether this node's DirEntry has been modified.
	dirty bool
}

// tree implements Tree.
type tree struct {
	mu      sync.Mutex // protects all accesses to the tree.
	user    upspin.UserName
	context upspin.Context
	log     Log
	root    *node
	// dirtyNodes array indices are the pathlen of the set of nodes in the map. The value of the map is ignored.
	dirtyNodes []map[*node]bool
}

var _ Tree = (*tree)(nil)

// Some common errors.
var (
	errNotImplemented        = errors.E(errors.Invalid, errors.Str("not implemented"))
	errInternalInconsistency = errors.Str("internal inconsistency")
)

// New creates an empty Tree for a user.
func New(user upspin.UserName, cfg *Config) Tree {
	if cfg == nil || cfg.Log == nil ||
		cfg.Context.StoreEndpoint().Transport == upspin.Unassigned ||
		cfg.Context.Factotum() == nil || cfg.Context.UserName() == "" ||
		cfg.Context.KeyEndpoint().Transport == upspin.Unassigned {
		log.Error.Printf("Tree.New: Invalid config for user %q: %v", user, cfg)
		return nil
	}
	return &tree{
		user:    user,
		context: cfg.Context.Copy(),
		log:     cfg.Log,
	}
}

// Lookup returns a DirEntry (de) that represents the path. The returned de may or may not
// have valid references inside. If dirty is true, the references are not up-to-date.
// Call Flush first to get an updated DirEntry.
func (t *tree) Lookup(path upspin.PathName) (de *upspin.DirEntry, dirty bool, err error) {
	// TODO
	return nil, false, nil
}

// Put puts a DirEntry de. If de overwrites a file, it is okay, but if it overwrites
// a directory an error will be returned.
func (t *tree) Put(de *upspin.DirEntry) error {
	const Put = "Put"
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := path.Parse(de.Name)
	if err != nil {
		return err
	}
	if p.IsRoot() {
		return t.createRoot(&p, de)
	}
	// If putting a/b/c/d, ensure a/b/c is loaded.
	parentPath := p.Drop(1)
	parent, err := t.ensurePathLoaded(&parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	// Now add this dirEntry as a new node
	node := &node{
		dirEntry: de,
	}
	err = t.addChild(node, &p, parent, &parentPath)
	if err != nil {
		return err
	}
	// Generate log entry.
	t.log.Append(de)
	return nil
}

// addChild is a helper function that adds a node n with path nodePath as
// the child of parent, whose path is parentPath.
func (t *tree) addChild(n *node, nodePath *path.Parsed, parent *node, parentPath *path.Parsed) error {
	if !parent.dirEntry.IsDir() {
		return errors.E(errors.NotDir, errors.Errorf("path: %q", parent.dirEntry.Name))
	}
	if parent.children == nil {
		parent.children = make(map[string]*node)
	}
	nElem := parentPath.NElem()
	if nodePath.NElem() != nElem+1 {
		log.Error.Printf("addChild: Child path must be exactly one element longer than parent.")
		return errInternalInconsistency
	}
	// No need to check if it exists. Simply overwrite. DirServer checks these things.
	parent.children[nodePath.Elem(nElem)] = n
	log.Printf("current children of %v: %#v", parent, parent.children)
	// Mark entire path as dirty.
	return t.markPathDirty(nodePath)
}

// markPathDirty marks the entire path from root to p as dirty.
func (t *tree) markPathDirty(p *path.Parsed) error {
	// Do we have room to track the max path depth in p?
	if len(t.dirtyNodes) < p.NElem()+1 {
		newDirtyNodes := make([]map[*node]bool, p.NElem()+1) // +1 for the root.
		copy(newDirtyNodes, t.dirtyNodes)
		t.dirtyNodes = newDirtyNodes
	}

	// Start with the root.
	n := t.root
	t.setNodeDirtyAt(0, n)

	// Navigate through every element of p.
	var err error
	for i := 0; i < p.NElem(); i++ {
		elem := p.Elem(i)
		n, err = t.loadNode(elem, n)
		if err != nil {
			return err
		}
		t.setNodeDirtyAt(i+1, n)
	}
	return nil
}

// setNodeDirtyAt sets the node as dirty and adds it to the dirtyNodes list at a given level.
// The dirtyNodes list is expected to be large enough to accommodate level entries.
func (t *tree) setNodeDirtyAt(level int, n *node) {
	n.dirty = true
	if t.dirtyNodes[level] == nil {
		t.dirtyNodes[level] = make(map[*node]bool)
	}
	t.dirtyNodes[level][n] = true // repetitions don't matter.
}

// ensurePathLoaded ensures the tree contains all nodes up to p and returns p's node.
// If any node is not already in memory, it is loaded from the store server.
func (t *tree) ensurePathLoaded(p *path.Parsed) (*node, error) {
	err := t.ensureRootLoaded()
	if err != nil {
		return nil, err
	}
	parent := t.root
	for i := 0; i < p.NElem(); i++ {
		node, err := t.loadNode(p.Elem(i), parent)
		if err != nil {
			return nil, err
		}
		parent = node
	}
	return parent, nil
}

// loadNode loads a child element of parent and returns that node, allocating it
// and loading it from storage if it's not already loaded.
func (t *tree) loadNode(elem string, parent *node) (*node, error) {
	if parent.children == nil {
		// Must load from disk.
		data, err := t.readDirEntry(parent.dirEntry)
		if err != nil {
			return nil, err
		}
		err = t.addChildren(parent, data)
		if err != nil {
			return nil, err
		}
	}
	for dirName, node := range parent.children {
		if elem == dirName {
			return node, nil
		}
	}
	return nil, errors.E(errors.NotExist, path.Join(parent.dirEntry.Name, elem))
}

// ensureRootLoaded loads the root into memory if it is not already loaded.
func (t *tree) ensureRootLoaded() error {
	if t.root != nil {
		return nil
	}
	rootDirEntry := t.log.Root()
	if rootDirEntry == nil {
		return errors.E(errors.NotExist, t.user)
	}
	t.root = &node{
		dirEntry: rootDirEntry,
	}
	return nil
}

// createRoot creates the root at p using the given dir entry. A root must not already exist.
func (t *tree) createRoot(p *path.Parsed, de *upspin.DirEntry) error {
	const createRoot = "createRoot"
	errRootExists := errors.E(createRoot, errors.Exist, errors.Str("root already created"))
	if t.root != nil {
		// Root already exists.
		return errRootExists
	}
	// Do we know how to find this root?
	if t.log.Root() != nil {
		// User root exists, just hasn't been loaded yet.
		return errRootExists
	}
	// To be sure, the log must be empty too (or t.root wouldn't be empty).
	if t.log.LastIndex() >= 0 {
		log.Error.Printf("Index not empty, but root not found.")
		return errInternalInconsistency
	}
	// Finally let's create it.
	node := &node{
		dirEntry: de,
	}
	t.root = node
	err := t.markPathDirty(p)
	if err != nil {
		return errors.E(createRoot, err)
	}
	t.log.Append(de)
	return nil
}

// Delete deletes the DirEntry associated with name.
func (t *tree) Delete(name upspin.PathName) error {
	const Delete = "Delete"
	t.mu.Lock()
	defer t.mu.Unlock()

	// TODO. (Make sure to remove from dirty blocks if removed DirEntry was not flushed yet).

	return errors.E(Delete, errNotImplemented)
}

// Flush flushes all dirty entries.
func (t *tree) Flush() error {
	const Flush = "Flush"
	t.mu.Lock()
	defer t.mu.Unlock()

	// Flush from highest path depth up to root.
	flushed := 0
	for i := len(t.dirtyNodes) - 1; i >= 0; i-- {
		m := t.dirtyNodes[i]
		if len(m) == 0 {
			// Nothing in map, nothing to do.
			continue
		}
		// For each node at level i, flush it.
		for n := range m {
			err := t.writeNode(n)
			if err != nil {
				return errors.E(Flush, err)
			}
			n.dirty = false
			flushed++
		}
	}
	// Throw away the entire slice of maps.
	t.dirtyNodes = nil

	// Verify the log had at least the same number of dirty entries (it could have more because of deletes).
	if t.log.LastIndex()+1 < flushed {
		return errors.E(Flush, errInternalInconsistency)
	}

	// Truncate the log.
	t.log.Drop(t.log.LastIndex())

	// Save new root in the log.
	t.log.SetRoot(t.root.dirEntry)

	return nil
}

// Close flushes the Tree to the Store and releases all resources.
func (t *tree) Close() error {
	const Close = "Close"
	t.mu.Lock()
	defer t.mu.Unlock()

	// TODO.

	return errors.E(Close, errNotImplemented)
}

// Root returns the root of the Tree.
func (t *tree) Root() *upspin.DirEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.log.Root()
}
