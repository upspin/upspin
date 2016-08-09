// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements the Tree interface declared in tree.go.

// TODO: fine-grained locking; crash recovery; log playback; metrics; performance tuning.

import (
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// node is an internal representation of a node in the tree.
// All node accesses must be protected the tree's mutex.
type node struct {
	// entry is the DirEntry this node represents.
	entry *upspin.DirEntry

	// kids maps a path element of a path name to the dir entries that represent them.
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
	// be held when calling all unexported methods.
	mu sync.Mutex

	user     upspin.UserName
	context  upspin.Context
	packer   upspin.Packer
	log      Log
	logIndex LogIndex
	root     *node
	// dirtyNodes is the set of dirty nodes, grouped by path length.
	// The index of the slice is the path length of the nodes therein.
	// The value of the map is ignored.
	dirtyNodes []map[*node]bool
}

var _ Tree = (*tree)(nil)

// String implements fmt.Stringer.
// t.mu must be held.
func (n *node) String() string {
	return fmt.Sprintf("node: %q, dirty: %v, kids: %d", n.entry.Name, n.dirty, len(n.kids))
}

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
	packer := pack.Lookup(cfg.Context.Packing())
	if packer == nil {
		log.Error.Printf("no packing %s registered", cfg.Context.Packing())
		return nil
	}
	return &tree{
		user:     user,
		context:  cfg.Context.Copy(),
		packer:   packer,
		log:      cfg.Log,
		logIndex: cfg.LogIndex,
	}
}

// Lookup returns a directory entry that represents the path.
// Dirty reports whether the entry is different from the stored version.
// The returned entry's references are not up-to-date if the entry is dirty.

func (t *tree) Lookup(name upspin.PathName) (de *upspin.DirEntry, dirty bool, err error) {
	const Lookup = "Lookup"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(name)
	if err != nil {
		return nil, false, errors.E(Lookup, err)
	}
	node, err := t.loadPath(p)
	if err != nil {
		return nil, false, errors.E(Lookup, err)
	}
	return node.entry, node.dirty, nil
}

// Put puts a DirEntry to the Store. Files may be overwritten,
// but attempts to put an existing directory will return an error.
func (t *tree) Put(de *upspin.DirEntry) error {
	const Put = "Put"
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := path.Parse(de.Name)
	if err != nil {
		return errors.E(Put, err)
	}
	if p.IsRoot() {
		return t.createRoot(p, de)
	}
	// If putting a/b/c/d, ensure a/b/c is loaded.
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	// Now add this dirEntry as a new node
	node := &node{
		entry: de,
	}
	err = t.addChild(node, p, parent, parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	// Generate log entry.
	return t.log.Append(de)
}

// addChild adds a node n with path nodePath as the child of parent, whose path is parentPath.
// t.mu must be held.
func (t *tree) addChild(n *node, nodePath path.Parsed, parent *node, parentPath path.Parsed) error {
	const addChild = "addChild"
	if !parent.entry.IsDir() {
		return errors.E(addChild, errors.NotDir, errors.Errorf("path: %q", parent.entry.Name))
	}
	if parent.kids == nil {
		parent.kids = make(map[string]*node)
	}
	nElem := parentPath.NElem()
	if nodePath.Drop(1).Path() != parentPath.Path() {
		err := errors.E(addChild, nodePath.Path(), errors.Internal, errors.Str("parent path does match parent of dir path"))
		log.Error.Printf("%s.", err)
		return err
	}
	// No need to check if it exists. Simply overwrite. DirServer checks these things.
	parent.kids[nodePath.Elem(nElem)] = n
	// Mark entire path as dirty.
	return t.markDirty(nodePath)
}

// markDirty marks the entire path from root to p as dirty.
// t.mu must be held.
func (t *tree) markDirty(p path.Parsed) error {
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
		n, err = t.loadNode(n, elem)
		if err != nil {
			return err
		}
		t.setNodeDirtyAt(i+1, n)
	}
	return nil
}

// setNodeDirtyAt sets the node as dirty and adds it to the dirtyNodes list at a given level.
// The dirtyNodes list is expected to be large enough to accommodate level entries.
// t.mu must be held.
func (t *tree) setNodeDirtyAt(level int, n *node) {
	n.dirty = true
	if t.dirtyNodes[level] == nil {
		t.dirtyNodes[level] = make(map[*node]bool)
	}
	t.dirtyNodes[level][n] = true // repetitions don't matter.
}

// loadPath ensures the tree contains all nodes up to p and returns p's node.
// If any node is not already in memory, it is loaded from the store server.
// t.mu must be held.
func (t *tree) loadPath(p path.Parsed) (*node, error) {
	err := t.loadRoot()
	if err != nil {
		return nil, err
	}
	node := t.root
	for i := 0; i < p.NElem(); i++ {
		node, err = t.loadNode(node, p.Elem(i))
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// loadNode loads a child node of parent with the given path-wise element name,
// loading it from storage if is not already loaded.
// t.mu must be held.
func (t *tree) loadNode(parent *node, elem string) (*node, error) {
	if parent.kids == nil {
		// Must load from store.
		data, err := t.readDirEntry(parent.entry)
		if err != nil {
			return nil, err
		}
		err = t.loadKidsFromBlock(parent, data)
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
// t.mu must be held.
func (t *tree) loadRoot() error {
	const loadRoot = "loadRoot"
	if t.root != nil {
		return nil
	}
	rootDirEntry, err := t.logIndex.Root()
	if err != nil {
		return errors.E(loadRoot, err)
	}
	if rootDirEntry == nil {
		return errors.E(loadRoot, errors.NotExist, t.user)
	}
	t.root = &node{
		entry: rootDirEntry,
	}
	return nil
}

// createRoot creates the root at p using the given dir entry. A root must not already exist.
// t.mu must be held.
func (t *tree) createRoot(p path.Parsed, de *upspin.DirEntry) error {
	const createRoot = "createRoot"
	// Check that we're trying to create a root for the owner of the Tree only.
	if p.User() != t.user {
		return errors.E(createRoot, p.User(), p.Path(), errors.Invalid, errors.Str("can't create root for another user"))
	}
	// Do we have a root already?
	_, err := t.logIndex.Root()
	if e, ok := err.(*errors.Error); !ok || e.Kind != errors.NotExist {
		// Error reading the root.
		return errors.E(createRoot, err)
	}
	if t.root != nil || err == nil {
		// Root already exists.
		return errors.E(createRoot, errors.Exist, errors.Str("root already created"))
	}
	// To be sure, the log must be empty too (or t.root wouldn't be empty).
	if t.log.LastIndex() >= 0 {
		err := errors.E(createRoot, errors.Internal, errors.Str("index not empty, but root not found"))
		log.Error.Printf("%s.", err)
		return err
	}
	// Finally let's create it.
	node := &node{
		entry: de,
	}
	t.root = node
	err = t.markDirty(p)
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

	p, err := path.Parse(name)
	if err != nil {
		return errors.E(Delete, err)
	}
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	if err != nil {
		return errors.E(Delete, err)
	}
	// Load the node of interest, which is the NElem-th element in its
	// parent's path.
	elem := p.Elem(parentPath.NElem())
	node, err := t.loadNode(parent, elem)
	if err != nil {
		// Can't load parent.
		return errors.E(Delete, err)
	}
	if len(node.kids) > 0 {
		// Node is a non-empty directory.
		return errors.E(Delete, errors.NotEmpty, p.Path())
	}
	// Remove this elem from the parent's kids' map.
	// No need to check if it was there -- it wouldn't have loaded if it weren't.
	delete(parent.kids, elem)

	// If node was dirty, there's no need to flush it to Store ever.
	t.removeFromDirtyList(p, node)

	// Update parent: mark it dirty and log its new version.
	err = t.markDirty(parentPath)
	if err != nil {
		// In practice this can't happen, since the entire path is
		// already loaded.
		return errors.E(Delete, err)
	}
	return t.log.Append(parent.entry)
}

// removeFromDirtyList removes a node n at path p from the list of dirty
// nodes, if n was there.
func (t *tree) removeFromDirtyList(p path.Parsed, n *node) {
	nElem := p.NElem()
	if nElem >= len(t.dirtyNodes) {
		// Dirty list does not even go this far. Nothing to do.
		return
	}
	m := t.dirtyNodes[nElem]
	delete(m, n)
}

// Flush flushes all dirty entries.
func (t *tree) Flush() error {
	const Flush = "Flush"
	t.mu.Lock()
	defer t.mu.Unlock()

	// Flush from highest path depth up to root.
	for i := len(t.dirtyNodes) - 1; i >= 0; i-- {
		m := t.dirtyNodes[i]
		// For each node at level i, flush it.
		for n := range m {
			err := t.store(n)
			if err != nil {
				return errors.E(Flush, err)
			}
			n.dirty = false
		}
	}
	// Throw away the entire slice of maps.
	t.dirtyNodes = nil

	// TODO: Verify the log had at least the same number of dirty entries
	// (it could have more because of deletes).

	// Save the last index we operated on.
	err := t.logIndex.SaveIndex(t.log.LastIndex())
	if err != nil {
		return errors.E(Flush, err)
	}

	// Save new root to the log index.
	return t.logIndex.SaveRoot(t.root.entry)
}

// Close flushes the Tree to the Store and releases all resources.
func (t *tree) Close() error {
	const Close = "Close"
	t.mu.Lock()
	defer t.mu.Unlock()

	err := t.Flush()
	if err != nil {
		return errors.E(Close, err)
	}

	return nil
}

// Root returns the root of the Tree.
func (t *tree) Root() (*upspin.DirEntry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.logIndex.Root()
}
