// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements the Tree interface declared in tree.go.

// TODO: fine-grained locking; better errors; more logging; metrics; performance tuning.

import (
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// node is an internal representation of a node in the tree.
// All node accesses must be protected the tree's mutex.
type node struct {
	// entry is the DirEntry this node represents.
	entry *upspin.DirEntry

	// kids maps a fragment of a path name to the dir entries that represent them.
	// It is empty if this node's dirEntry represents a file or an empty directory;
	// if it represents a directory, kids holds the memory-loaded subdir nodes
	// (not all subdir nodes may be in-memory at a given time).
	kids map[string]*node

	// dirty indicates whether this node's DirEntry has been modified
	// since it was last written to the store.
	dirty bool
}

// tree implements Tree.
type tree struct {
	// mu protects all accesses to the tree and its nodes and must
	// be held when calling all methods.
	mu sync.Mutex

	user    upspin.UserName
	context upspin.Context
	packer  upspin.Packer
	log     Log
	root    *node
	// dirtyNodes is the set of dirty nodes, grouped by path length.
	// The index of the slice is the path length of the nodes therein.
	// The value of the map is ignored.
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
	// TODO: split error cases and maybe return an error too.
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

// Lookup returns a directory entry that represents the path.
// Dirty reports whether the entry is different than the stored version.
// The returned entry's references are not up-to-date if the entry is dirty.
// Call Flush first to get an updated entry.
func (t *tree) Lookup(path upspin.PathName) (de *upspin.DirEntry, dirty bool, err error) {
	// TODO
	return nil, false, errNotImplemented
}

// Put puts a DirEntry to the Store. If the entrye overwrites a file, that is fine,
// but if it overwrites a directory an error will be returned.
func (t *tree) Put(de *upspin.DirEntry) error {
	const Put = "Put"
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := path.Parse(de.Name)
	if err != nil {
		return errors.E(Put, err)
	}
	if p.IsRoot() {
		return t.createRoot(&p, de)
	}
	// If putting a/b/c/d, ensure a/b/c is loaded.
	parentPath := p.Drop(1)
	parent, err := t.loadPath(&parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	// Now add this dirEntry as a new node
	node := &node{
		entry: de,
	}
	err = t.addChild(node, &p, parent, &parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	// Generate log entry.
	return t.log.Append(de)
}

// addChild adds a node n with path nodePath as the child of parent, whose path is parentPath.
func (t *tree) addChild(n *node, nodePath *path.Parsed, parent *node, parentPath *path.Parsed) error {
	if !parent.entry.IsDir() {
		return errors.E(errors.NotDir, errors.Errorf("path: %q", parent.entry.Name))
	}
	if parent.kids == nil {
		parent.kids = make(map[string]*node)
	}
	nElem := parentPath.NElem()
	if nodePath.NElem() != nElem+1 {
		log.Error.Printf("addChild: Child path must be exactly one element longer than parent.")
		return errInternalInconsistency
	}
	// No need to check if it exists. Simply overwrite. DirServer checks these things.
	parent.kids[nodePath.Elem(nElem)] = n
	// Mark entire path as dirty.
	return t.markDirty(nodePath)
}

// markDirty marks the entire path from root to p as dirty.
func (t *tree) markDirty(p *path.Parsed) error {
	// Do we have room to track the max path depth in p?
	if n := p.NElem() + 1; len(t.dirtyNodes) < n { // +1 for the root.
		newDirtyNodes := make([]map[*node]bool, n)
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

// loadPath ensures the tree contains all nodes up to p and returns p's node.
// If any node is not already in memory, it is loaded from the store server.
func (t *tree) loadPath(p *path.Parsed) (*node, error) {
	err := t.loadRoot()
	if err != nil {
		return nil, err
	}
	node := t.root
	for i := 0; i < p.NElem(); i++ {
		node, err = t.loadNode(p.Elem(i), node)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// loadNode loads a child node of parent with the given path-wise element name,
// loading it from storage if is not already loaded.
func (t *tree) loadNode(elem string, parent *node) (*node, error) {
	if parent.kids == nil {
		// Must load from store.
		data, err := t.readDirEntry(parent.entry)
		if err != nil {
			return nil, err
		}
		err = t.addChildren(parent, data)
		if err != nil {
			return nil, err
		}
	}
	for dirName, node := range parent.kids {
		if elem == dirName {
			return node, nil
		}
	}
	return nil, errors.E(errors.NotExist, path.Join(parent.entry.Name, elem))
}

// loadRoot loads the root into memory if it is not already loaded.
func (t *tree) loadRoot() error {
	if t.root != nil {
		return nil
	}
	rootDirEntry := t.log.Root()
	if rootDirEntry == nil {
		return errors.E(errors.NotExist, t.user)
	}
	t.root = &node{
		entry: rootDirEntry,
	}
	return nil
}

// createRoot creates the root at p using the given dir entry. A root must not already exist.
func (t *tree) createRoot(p *path.Parsed, de *upspin.DirEntry) error {
	const createRoot = "createRoot"
	if t.root != nil || t.log.Root() != nil {
		// Root already exists.
		return errors.E(createRoot, errors.Exist, errors.Str("root already created"))
	}
	// To be sure, the log must be empty too (or t.root wouldn't be empty).
	if t.log.LastIndex() >= 0 {
		log.Error.Printf("Index not empty, but root not found.")
		return errInternalInconsistency
	}
	// Finally let's create it.
	node := &node{
		entry: de,
	}
	t.root = node
	err := t.markDirty(p)
	if err != nil {
		return errors.E(createRoot, err)
	}
	return t.log.Append(de)
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
			err := t.store(n)
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
	err := t.log.Drop(t.log.LastIndex())
	if err != nil {
		return errors.E(Flush, err)
	}

	// Save new root in the log.
	t.log.SetRoot(t.root.entry)

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

func (n *node) String() string {
	return fmt.Sprintf("node: %q, dirty: %v, numKids: %d", n.entry.Name, n.dirty, len(n.kids))
}
