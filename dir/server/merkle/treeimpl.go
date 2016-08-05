// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle

import (
	"time"

	"sync"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// This file implements the Tree interface listed in merkle.go.

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
	mu      sync.Mutex // protects all accesses to the tree. (TODO: maybe relax this later)
	user    upspin.UserName
	context upspin.Context
	log     Log
	root    *node
}

var (
	errNotImplemented = errors.E(errors.Invalid, errors.Str("not implemented"))
)

// New creates an empty Tree for a user.
func New(user upspin.UserName, cfg *Config) Tree {
	if cfg == nil || cfg.Log == nil || cfg.StoreEndpoint.Transport == upspin.Unassigned ||
		cfg.Factotum == nil || cfg.ServerName == "" {
		log.Error.Printf("Tree.New: Invalid config for user %q", user)
		return nil
	}
	return &tree{
		user:    user,
		context: context.New().SetFactotum(cfg.Factotum).SetUserName(cfg.ServerName).SetStoreEndpoint(cfg.StoreEndpoint),
		log:     cfg.Log,
	}
}

// Lookup returns a DirEntry (de) that represents the path. The returned de may or may not
// have valid references inside. If dirty is true, the references are not up-to-date.
// Call Flush first to get an updated DirEntry.
func (t *tree) Lookup(path upspin.PathName) (de *upspin.DirEntry, dirty bool, err error) {
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
		return t.handleRootCreation(&p, de)
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
		dirty:    true,
	}
	err = t.addChild(node, p, parent, parentPath)
	if err != nil {
		return err
	}
	// Generate log entry.
	t.log.Append(de)
	return nil
}

func (t *tree) addChild(n *node, nodePath *path.Parsed, parent *node, parentPath *path.Parsed) error {
	if !parent.dirEntry.IsDir() {
		return errors.E(errors.NotDir, errors.Errorf("path: %q", parent.dirEntry.Name))
	}
	if parent.children == nil {
		parent.children = make(map[string]*node)
	}
	nElem := parentPath.NElem() + 1
	if nodePath.NElem() != nElem {
		log.Error.Printf("addChild: Child path must be exactly one element longer than parent.")
		return errInternalInconsistency
	}
	// No need to check if it exists. Simply overwrite. DirServer checks these things.
	parent.children[nodePath.Elem(nElem)] = n
	// Mark all path as dirty.
	t.markPathDirty(nodePath)
	return nil
}

// markPathDirty marks the entire path from root to p as dirty.
func (t *tree) markPathDirty(p *path.Parsed) error {
	n := t.root
	n.dirty = true
	var err error
	for i := 1; i < p.NElem(); i++ {
		elem := p.Elem(i)
		n, err = t.loadNode(elem, n)
		if err != nil {
			return err
		}
		n.dirty = true
	}
	return nil
}

// ensurePathLoaded ensures the tree contains all nodes up to p and returns its node.
// If any node is not already in memory, it is loaded from the store server.
func (t *tree) ensurePathLoaded(p *path.Parsed) (*node, error) {
	err := t.ensureRootLoaded()
	if err != nil {
		return nil, err
	}
	parent := t.root
	for i := 1; i < p.NElem(); i++ {
		node, err := t.loadNode(p.Elem(i), parent)
		if err != nil {
			return nil, err
		}
		parent = node
	}
	return parent, nil
}

// loadNode loads a child element of parent and returns that node, allocating it
// and loading it from storage if not already loaded.
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
	return nil, errors.E("loadNode", errors.NotExist, errors.Errorf("path: %s/%s", parent.dirEntry.Name, elem))
}

// ensureRootLoaded loads the root into memory if it is not already loaded.
func (t *tree) ensureRootLoaded() error {
	if t.root != nil {
		return nil
	}
	loc := t.log.Root()
	blob, err := t.getLocation(&loc)
	if err != nil {
		return err
	}
	root := new(upspin.DirEntry)
	remainder, err := root.Unmarshal(blob)
	if err != nil {
		return err
	}
	if len(remainder) > 0 {
		return errors.E("ensureRootLoaded", errors.IO, errors.Str("too many bytes for root"))
	}
	node := t.newNode(root)
	t.root = node
	return nil
}

// newNode creates a new standalone node for the DirEntry.
func (t *tree) newNode(de *upspin.DirEntry) *node {
	node := &node{
		dirEntry: de,
	}
	return node
}

func (t *tree) handleRootCreation(p *path.Parsed, de *upspin.DirEntry) error {
	errRootExists := errors.E("Put", errors.Exist, errors.Str("root already created"))
	if t.root != nil {
		// Root already exists.
		return errRootExists
	}
	// Do we know how to find this root?
	loc := t.log.Root()
	if loc != (upspin.Location{}) {
		// User root exists, just hasn't been loaded yet.
		return errRootExists
	}
	// To be sure, the log must be empty too (or t.root wouldn't be empty).
	if t.log.LastIndex() != 0 {
		log.Error.Printf("Index not empty, but root not found.")
		return errInternalInconsistency
	}
	// Finally let's create it.
	node := &node{
		dirEntry: de,
		children: make(map[string]*node),
		dirty:    true,
	}
	t.root = node
	t.log.Append(de)
	return nil
}

// Delete deletes the DirEntry associated with name.
func (t *tree) Delete(name upspin.PathName) error {
	const Delete = "Delete"
	t.mu.Lock()
	defer t.mu.Unlock()

	return errors.E(Delete, errNotImplemented)
}

// Flush flushes all dirty entries.
func (t *tree) Flush() error {
	const Flush = "Flush"
	t.mu.Lock()
	defer t.mu.Unlock()

	return errors.E(Flush, errNotImplemented)
}

func (t *tree) Close() error {
	const Close = "Close"
	t.mu.Lock()
	defer t.mu.Unlock()

	return errors.E(Close, errNotImplemented)
}

func (t *tree) Root() upspin.Location {
	const Root = "Root"
	t.mu.Lock()
	defer t.mu.Unlock()

	// TODO
	return upspin.Location{}
}
