// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file implements the Tree interface declared in tree.go.

// TODO: fine-grained locking; crash recovery; log playback; metrics; performance tuning.

import (
	"bytes"
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
	entry upspin.DirEntry

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
	t := &tree{
		user:     user,
		context:  cfg.Context.Copy(),
		packer:   packer,
		log:      cfg.Log,
		logIndex: cfg.LogIndex,
	}
	// Do we have entries in the log to process, to recover from a crash?
	err := t.recoverFromLog()
	if err != nil {
		return nil
	}
	return t
}

// Lookup returns a directory entry that represents the path.
// Dirty reports whether the entry is different from the stored version.
// The returned entry's references are not up-to-date if the entry is dirty.

func (t *tree) Lookup(name upspin.PathName) (de upspin.DirEntry, dirty bool, err error) {
	const Lookup = "Lookup"
	var de0 upspin.DirEntry
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(name)
	if err != nil {
		return de0, false, errors.E(Lookup, err)
	}
	node, err := t.loadPath(p)
	if err != nil {
		return de0, false, errors.E(Lookup, err)
	}
	return node.entry, node.dirty, nil
}

// Put puts a DirEntry to the Store. Files may be overwritten,
// but attempts to put an existing directory will return an error.
func (t *tree) Put(de *upspin.DirEntry) error {
	const put = "Put"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(de.Name)
	if err != nil {
		return errors.E(put, err)
	}
	if p.IsRoot() {
		return t.createRoot(put, de)
	}
	err = t.put(p, de)
	if err != nil {
		return err
	}

	// Generate log entry.
	return t.log.Append(&LogEntry{
		Op:    Put,
		Entry: de,
	})
}

// put implements the bulk of Tree.Put, but does not append to the log so it
// can be used to recover from the Tree's state from the log.
func (t *tree) put(p path.Parsed, de *upspin.DirEntry) error {
	const Put = "Put"
	// If putting a/b/c/d, ensure a/b/c is loaded.
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	log.Printf("put: %v", de)
	if err != nil {
		return errors.E(Put, err)
	}
	// Now add this dirEntry as a new node
	node := &node{
		entry: *de,
	}
	err = t.addKid(node, p, parent, parentPath)
	if err != nil {
		return errors.E(Put, err)
	}
	return nil
}

// addKid adds a node n with path nodePath as the kid of parent, whose path is parentPath.
// t.mu must be held.
func (t *tree) addKid(n *node, nodePath path.Parsed, parent *node, parentPath path.Parsed) error {
	const addKid = "addKid"
	if !parent.entry.IsDir() {
		return errors.E(addKid, errors.NotDir, errors.Errorf("path: %q", parent.entry.Name))
	}
	if parent.kids == nil {
		// A directory with no kids. If it's dirty, it's new.
		// If it's not dirty, load it from Store.
		if parent.dirty {
			parent.kids = make(map[string]*node)
		} else {
			t.loadKids(parent)
		}
	}
	nElem := parentPath.NElem()
	if nodePath.Drop(1).Path() != parentPath.Path() {
		err := errors.E(addKid, nodePath.Path(), errors.Internal, errors.Str("parent path does match parent of dir path"))
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
		log.Printf("loadPath: loaded node: %v", node)
	}
	return node, nil
}

// loadNode loads a child node of parent with the given path-wise element name,
// loading it from storage if is not already loaded.
// t.mu must be held.
func (t *tree) loadNode(parent *node, elem string) (*node, error) {
	if parent.kids == nil {
		// Must load from store.
		err := t.loadKids(parent)
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

// loadKids loads all kids of a parent node from the Store.
func (t *tree) loadKids(parent *node) error {
	log.Printf("loading kids from Store for %q", parent.entry.Name)
	data, err := t.readDirEntry(&parent.entry)
	if err != nil {
		return err
	}
	err = t.loadKidsFromBlock(parent, data)
	if err != nil {
		return err
	}
	return nil
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
		entry: *rootDirEntry,
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
	if t.log.LastOffset() != 0 {
		err := errors.E(createRoot, errors.Internal, errors.Str("index not empty, but root not found"))
		log.Error.Printf("%s.", err)
		return err
	}
	// Finally let's create it.
	node := &node{
		entry: *de,
	}
	t.root = node
	err = t.markDirty(p)
	if err != nil {
		return errors.E(createRoot, err)
	}
	log.Printf("Created root: %v", t.root)
	return t.log.Append(&LogEntry{
		Op:    Put,
		Entry: de,
	})
}

// Delete deletes the DirEntry associated with name.
func (t *tree) Delete(name upspin.PathName) error {
	const delete = "Delete"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(name)
	if err != nil {
		return errors.E(delete, err)
	}
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	if err != nil {
		return errors.E(delete, err)
	}
	// Load the node of interest, which is the NElem-th element in its
	// parent's path.
	elem := p.Elem(parentPath.NElem())
	node, err := t.loadNode(parent, elem)
	if err != nil {
		// Can't load parent.
		return errors.E(delete, err)
	}
	if len(node.kids) > 0 {
		// Node is a non-empty directory.
		return errors.E(delete, errors.NotEmpty, p.Path())
	}
	// Remove this elem from the parent's kids map.
	// No need to check if it was there -- it wouldn't have loaded if it weren't.
	delete(parent.kids, elem)

	// If node was dirty, there's no need to flush it to Store ever.
	t.removeFromDirtyList(p, node)

	// Update parent: mark it dirty and log its new version.
	err = t.markDirty(parentPath)
	if err != nil {
		// In practice this can't happen, since the entire path is
		// already loaded.
		return errors.E(delete, err)
	}
	return t.log.Append(&LogEntry{
		Op:    Delete,
		Entry: *node.entry,
	})
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
	err := t.logIndex.SaveOffset(t.log.LastOffset())
	if err != nil {
		return errors.E(Flush, err)
	}

	// Save new root to the log index.
	return t.logIndex.SaveRoot(&t.root.entry)
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

// recoverFromLog inspects the LogIndex and the Log and replays the missing
// operations. This is only called from a single-thread context.
func (t *tree) recoverFromLog() error {
	const (
		recoverFromLog = "recoverFromLog"
		batchSize      = 10 // max number of entries to recover at a time.
	)
	lastOffset := t.log.LastOffset()
	lastProcessed, err := t.logIndex.ReadOffset()
	if err != nil {
		return err
	}
	if lastOffset == lastProcessed {
		// All caught up.
		log.Debug.Printf("Tree is all caught up for user %s", t.user)
		return nil
	}
	err = t.loadRoot()
	if err != nil {
		return errors.E(recoverFromLog, err)
	}

	// If not, replay all entries from the log. Read in chunks of
	// batchSizes entries at a time (a balance between efficiency and how
	// long we want to process the log without checkpointing our state).
	recovered := 0
	for {
		log.Printf("Going to recover from log...")
		replay, next, err := t.log.Read(lastProcessed, batchSize)
		if err != nil {
			return errors.E(recoverFromLog, err)
		}
		for _, logEntry := range replay {
			de := logEntry.Entry

			p, err := path.Parse(de.Name)
			if err != nil {
				// We don't expect this to fail because
				// de.Name was in the log already and thus
				// has been validated.
				return errors.E(recoverFromLog, err)
			}

			switch logEntry.Op {
			case Put:
				log.Printf("Going to put de: %v", de.Name)
				err = t.put(p, &de)
				if err != nil {
					// Now we're in serious trouble. We can't recover.
					log.Error.Printf("Can't recover from logs for user %s: %s", t.user, err)
					return errors.E(recoverFromLog, err)
				}
			case Delete:
				// TODO:
			default:
				return errors.E(recoverFromLog, errors.Internal, errors.Errorf("no such log operation: %v", logEntry.Op))
			}
		}
		// Update the log index so if we crash now, at least we processed
		// something already.
		err = t.logIndex.SaveOffset(next)
		if err != nil {
			// Can't update log index. Something is really bad.
			// TODO: retry?
			return errors.E(recoverFromLog, err)
		}
		recovered += len(replay)
		if len(replay) < batchSize {
			break
		}
	}
	log.Printf("%s: %d entries recovered. Tree is current.", recoverFromLog, recovered)
	log.Printf("Tree:\n%s\n", t.String())
	return nil
}

// String implements fmt.Stringer.
// t.mu must be held.
func (t *tree) String() string {
	var buf bytes.Buffer
	t.loadRoot()
	printNode(t.root, &buf)
	return buf.String()
}

func printNode(n *node, buf *bytes.Buffer) {
	if len(n.kids) == 0 {
		buf.WriteString(string(n.entry.Name) + "\n")
		return
	}
	for _, kid := range n.kids {
		printNode(kid, buf)
	}
}
