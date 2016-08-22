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

	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/valid"
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

// New creates an empty Tree using the server's context, a Log and a
// LogIndex for a particular user's tree. Context is used for contacting
// StoreServer, defining the default packing and setting the server name.
// All fields of the context must be defined. Log manipulates the log on behalf
// of the tree for a user. LogIndex is used by Tree to track the most recent
// changes stored in the log for the user. The user name in Log and LogIndex
// must be for the exact same user. If there are unprocessed log entries in
// the Log, the Tree's state is recovered from it.
// TODO: Maybe new is doing too much work. Figure out how to break in two without
// returning an inconsistent new tree if log is unprocessed.
func New(context upspin.Context, log Log, logIndex LogIndex) (Tree, error) {
	const op = "dir/server/tree.New"
	if context == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("context is nil"))
	}
	if log == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("log is nil"))
	}
	if logIndex == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("logIndex is nil"))
	}
	if context.StoreEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unassigned store endpoint"))
	}
	if context.KeyEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unassigned key endpoint"))
	}
	if context.Factotum() == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("factotum is nil"))
	}
	if context.UserName() == "" {
		return nil, errors.E(op, errors.Invalid, errors.Str("username in tree context is empty"))
	}
	if log.User() == "" {
		return nil, errors.E(op, errors.Invalid, errors.Str("username in log is empty"))
	}
	if log.User() != logIndex.User() {
		return nil, errors.E(op, errors.Invalid, errors.Str("username in log and logIndex mismatch"))
	}
	if err := valid.UserName(log.User()); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}
	packer := pack.Lookup(context.Packing())
	if packer == nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("no packing %s registered", context.Packing()))
	}
	t := &tree{
		user:     log.User(),
		context:  context.Copy(),
		packer:   packer,
		log:      log,
		logIndex: logIndex,
	}
	// Do we have entries in the log to process, to recover from a crash?
	err := t.recoverFromLog()
	if err != nil {
		return nil, errors.E(op, err)
	}
	return t, nil
}

// Lookup returns a directory entry that represents the path.
// Dirty reports whether the entry is different from the stored version.
// The returned entry's references are not up-to-date if the entry is dirty.
// See full description on interface on tree.go.
func (t *tree) Lookup(name upspin.PathName) (de *upspin.DirEntry, dirty bool, err error) {
	const op = "dir/server/tree.Lookup"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(name)
	if err != nil {
		return nil, false, errors.E(op, err)
	}
	node, err := t.loadPath(p)
	if err == upspin.ErrFollowLink {
		return &node.entry, node.dirty, err
	}
	if err != nil {
		return nil, false, errors.E(op, err)
	}
	return &node.entry, node.dirty, nil
}

// Put puts a DirEntry into the Tree.
// See full description on interface in tree.go.
func (t *tree) Put(de *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/server/tree.Put"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(de.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if p.IsRoot() {
		return nil, t.createRoot(p, de)
	}
	node, err := t.put(p, de)
	if err == upspin.ErrFollowLink {
		return &node.entry, err
	}
	if err != nil {
		return nil, err
	}

	// Generate log entry.
	return de, t.log.Append(&LogEntry{
		Op:    Put,
		Entry: *de,
	})
}

// put implements the bulk of Tree.Put, but does not append to the log so it
// can be used to recover the Tree's state from the log.
// t.mu must be held.
func (t *tree) put(p path.Parsed, de *upspin.DirEntry) (*node, error) {
	const op = "dir/server/tree.put"
	// If putting a/b/c/d, ensure a/b/c is loaded.
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	if err == upspin.ErrFollowLink { // encountered a link along the path.
		return parent, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if parent.entry.IsLink() {
		return parent, upspin.ErrFollowLink
	}
	// Now add this dirEntry as a new node
	node := &node{
		entry: *de,
	}
	err = t.addKid(node, p, parent, parentPath)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return node, nil
}

// addKid adds a node n with path nodePath as the kid of parent, whose path is parentPath.
// t.mu must be held.
func (t *tree) addKid(n *node, nodePath path.Parsed, parent *node, parentPath path.Parsed) error {
	const op = "dir/server/tree.addKid"
	if !parent.entry.IsDir() {
		return errors.E(op, errors.NotDir, errors.Errorf("path: %q", parent.entry.Name))
	}
	if parent.kids == nil {
		// This is a directory with no kids. If it's dirty, it's new.
		// If it's not dirty, load kids from Store.
		if parent.dirty {
			parent.kids = make(map[string]*node)
		} else {
			err := t.loadKids(parent)
			if err != nil {
				return errors.E(op, err)
			}
		}
	}
	nElem := parentPath.NElem()
	if nodePath.Drop(1).Path() != parentPath.Path() {
		err := errors.E(op, nodePath.Path(), errors.Internal, errors.Str("parent path does match parent of dir path"))
		log.Error.Print(err)
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
		// Non-directory entries are never marked dirty by the Tree,
		// only their parents (directories), which have their kids'
		// names and references packed in them.
		if !n.entry.IsDir() {
			continue
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
// If while loading the path a link is discovered, the link is returned and if
// it's not the last element of the path, ErrFollowLink is returned.
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
			return node, err // err could be upspin.ErrFollowLink.
		}
	}
	return node, nil
}

// loadDir loads the contents of a directory's node if it's not already loaded.
// The node must be known to be a directory and cannot be a link.
// t.mu must be held.
func (t *tree) loadDir(dir *node) error {
	// Must load from store if kids are not loaded. However, if it's dirty,
	// we have the most recent version, so no point in loading it.
	if dir.kids == nil && !dir.dirty {
		err := t.loadKids(dir)
		if err != nil {
			return err
		}
	}
	return nil
}

// loadNode loads a child node of parent with the given path-wise element name,
// loading it from storage if is not already loaded. If the parent node is a
// link, ErrFollowLink is returned, along with the parent node itself.
// t.mu must be held.
func (t *tree) loadNode(parent *node, elem string) (*node, error) {
	if parent.entry.IsLink() {
		return parent, upspin.ErrFollowLink
	}
	if !parent.entry.IsDir() {
		return nil, errors.E(errors.NotExist, path.Join(parent.entry.Name, elem))
	}
	err := t.loadDir(parent)
	if err != nil {
		return nil, err
	}
	for dirName, node := range parent.kids {
		if elem == dirName {
			return node, nil
		}
	}
	return nil, errors.E(errors.NotExist, path.Join(parent.entry.Name, elem))
}

// loadKids loads all kids of a parent node from the Store.
// t.mu must be held.
func (t *tree) loadKids(parent *node) error {
	log.Debug.Printf("Loading kids from Store for %q", parent.entry.Name)
	data, err := clientutil.ReadAll(t.context, &parent.entry)
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
	const op = "dir/server/tree.loadRoot"
	if t.root != nil {
		return nil
	}
	rootDirEntry, err := t.logIndex.Root()
	if err != nil {
		return errors.E(op, err)
	}
	if rootDirEntry == nil {
		return errors.E(op, errors.NotExist, t.user)
	}
	t.root = &node{
		entry: *rootDirEntry,
	}
	return nil
}

// createRoot creates the root at p using the given dir entry. A root must not already exist.
// t.mu must be held.
func (t *tree) createRoot(p path.Parsed, de *upspin.DirEntry) error {
	const op = "dir/server/tree.createRoot"
	// Check that we're trying to create a root for the owner of the Tree only.
	if p.User() != t.user {
		return errors.E(op, p.User(), p.Path(), errors.Invalid, errors.Str("can't create root for another user"))
	}
	// Do we have a root already?
	_, err := t.logIndex.Root()
	if e, ok := err.(*errors.Error); !ok || e.Kind != errors.NotExist {
		// Error reading the root.
		return errors.E(op, err)
	}
	if t.root != nil || err == nil {
		// Root already exists.
		return errors.E(op, errors.Exist, errors.Str("root already created"))
	}
	// To be sure, the log must be empty too (or t.root wouldn't be empty).
	if t.log.LastOffset() != 0 {
		err := errors.E(op, errors.Internal, errors.Str("index not empty, but root not found"))
		log.Error.Print(err)
		return err
	}
	// Finally let's create it.
	node := &node{
		entry: *de,
	}
	t.root = node
	err = t.markDirty(p)
	if err != nil {
		return errors.E(op, err)
	}
	// The root of the tree must be flushed immediately or its recovery
	// becomes cumbersome. Nothing else exists prior to a root existing,
	// so only the root will be flushed.
	log.Printf("Created root: %v", t.root)
	return t.flush()
}

// List lists the contents of a prefix. If prefix names a directory, all
// entries of the directory are returned. If prefix names a file, that
// file's entry is returned. List does not interpret wildcards. Dirty reports
// whether any DirEntry returned is dirty (and thus may contain outdated
// references).
func (t *tree) List(prefix path.Parsed) ([]*upspin.DirEntry, bool, error) {
	const op = "dir/server/tree.List"
	t.mu.Lock()
	defer t.mu.Unlock()

	node, err := t.loadPath(prefix)
	if err == upspin.ErrFollowLink {
		return []*upspin.DirEntry{&node.entry}, node.dirty, err
	}
	if err != nil {
		return nil, false, errors.E(op, err)
	}
	if !node.entry.IsDir() {
		return []*upspin.DirEntry{&node.entry}, node.dirty, err
	}
	err = t.loadDir(node)
	if err != nil {
		return nil, false, errors.E(op, err)
	}
	dirty := node.dirty
	var entries []*upspin.DirEntry
	for _, n := range node.kids {
		entries = append(entries, &n.entry)
	}
	return entries, dirty, nil
}

// Delete deletes the DirEntry associated with name.
// See full description on interface in tree.go.
func (t *tree) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/server/tree.Delete"
	t.mu.Lock()
	defer t.mu.Unlock()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	node, err := t.delete(p)
	if err == upspin.ErrFollowLink {
		return &node.entry, err
	}
	if err != nil {
		return nil, err
	}
	return nil, t.log.Append(&LogEntry{
		Op:    Delete,
		Entry: node.entry,
	})
}

// delete implements the bulk of Tree.Delete, but does not append to the log
// so it can be used to recover from the Tree's state from the log.
// t.mu must be held.
func (t *tree) delete(p path.Parsed) (*node, error) {
	const op = "dir/server/tree.delete"
	parentPath := p.Drop(1)
	parent, err := t.loadPath(parentPath)
	if err == upspin.ErrFollowLink {
		return parent, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Load the node of interest, which is the NElem-th element in its
	// parent's path.
	elem := p.Elem(parentPath.NElem())
	node, err := t.loadNode(parent, elem)
	if err == upspin.ErrFollowLink {
		return node, err
	}
	if err != nil {
		// Can't load parent.
		return nil, errors.E(op, err)
	}
	if len(node.kids) > 0 {
		// Node is a non-empty directory.
		return nil, errors.E(op, errors.NotEmpty, p.Path())
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
		return nil, errors.E(op, err)
	}
	return node, nil
}

// removeFromDirtyList removes a node n at path p from the list of dirty
// nodes, if n was there.
// t.mu must be held.
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
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.flush()
}

// flush flushes all dirty entries.
// t.mu must be held.
func (t *tree) flush() error {
	const op = "dir/server/tree.Flush"
	// Flush from highest path depth up to root.
	for i := len(t.dirtyNodes) - 1; i >= 0; i-- {
		m := t.dirtyNodes[i]
		// For each node at level i, flush it.
		for n := range m {
			err := t.store(n)
			if err != nil {
				return errors.E(op, err)
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
		return errors.E(op, err)
	}

	// Save new root to the log index.
	return t.logIndex.SaveRoot(&t.root.entry)
}

// Close flushes the Tree to the Store and releases all resources.
func (t *tree) Close() error {
	const op = "dir/server/tree.Close"
	t.mu.Lock()
	defer t.mu.Unlock()

	err := t.Flush()
	if err != nil {
		return errors.E(op, err)
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
// operations. It can only be called from New.
func (t *tree) recoverFromLog() error {
	const (
		op        = "dir/server/tree.recoverFromLog"
		batchSize = 10 // max number of entries to recover at a time.
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
		return errors.E(op, err)
	}

	// Tree is not current. Replay all entries from the log. Read in chunks
	// of batchSizes entries at a time (a balance between efficiency and
	// how long we want to process the log without checkpointing our state).
	recovered := 0
	next := lastProcessed
	for {
		log.Debug.Printf("Recovering from log...")
		var replay []LogEntry
		replay, next, err = t.log.ReadAt(batchSize, next)
		if err != nil {
			return errors.E(op, err)
		}
		for _, logEntry := range replay {
			de := logEntry.Entry

			p, err := path.Parse(de.Name)
			if err != nil {
				// We don't expect this to fail because
				// de.Name was in the log already and thus
				// has been validated.
				return errors.E(op, err)
			}

			switch logEntry.Op {
			case Put:
				log.Debug.Printf("Putting dirEntry: %q", de.Name)
				_, err = t.put(p, &de)
			case Delete:
				log.Debug.Printf("Deleting path: %q", p.Path())
				_, err = t.delete(p)
			default:
				return errors.E(op, errors.Internal, errors.Errorf("no such log operation: %v", logEntry.Op))
			}
			if err != nil {
				// Now we're in serious trouble. We can't recover.
				log.Error.Printf("Can't recover from logs for user %s: %s", t.user, err)
				return errors.E(op, err)
			}
		}
		recovered += len(replay)
		if len(replay) < batchSize {
			break
		}
	}
	log.Printf("%s: %d entries recovered. Tree is current.", op, recovered)
	log.Debug.Printf("Tree:\n%s\n", t.String())
	return nil
}

// OnEviction implements cache.EvictionNotifier.
func (t *tree) OnEviction(key interface{}) {
	log.Debug.Printf("Tree being evicted: %s", t.log.User())
	err := t.Flush()
	if err != nil {
		log.Error.Printf("OnEviction: Flush: %v", err)
	}
}

// String implements fmt.Stringer.
// t.mu must be held.
func (t *tree) String() string {
	var buf bytes.Buffer
	t.loadRoot()
	printNode(t.root, &buf)
	return buf.String()
}

// printNode traverses the tree depth-first and appends each node to the buffer.
// It supports method String.
func printNode(n *node, buf *bytes.Buffer) {
	if len(n.kids) == 0 {
		buf.WriteString(string(n.entry.Name) + "\n")
		return
	}
	for _, kid := range n.kids {
		printNode(kid, buf)
	}
}
