// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements DirServer using a Tree as backing.
package server

import (
	"io/ioutil"
	"strconv"

	"upspin.io/access"
	"upspin.io/cache"
	"upspin.io/dir/server/tree"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/valid"
)

var (
	// TODO: delete once everything is implemented.
	errNotImplemented = errors.Str("not implemented")

	// errNotExist is only used for comparison, to detect whether entries
	// already exist.
	errNotExist = errors.E(errors.NotExist)
)

const (
	// entryMustBeClean is used with lookup to specify whether the caller
	// needs to look at the dir entry's references and therefore whether the
	// tree must be flushed if a dirty dir entry is found.
	entryMustBeClean = true
)

// server implements upspin.DirServer.
type server struct {
	// serverContext holds this server's Factotum, server name and store
	// endpoint where to store dir entries. It is set when the server is
	// first registered and never reset again.
	serverContext upspin.Context

	// userName is the name of the user on behalf of whom this
	// server is serving.
	userName upspin.UserName

	// logDir is the directory path accessible through the local file system
	// where user logs are stored.
	logDir string

	// userTrees keeps track of user trees in LRU fashion, where key
	// is an upspin.UserName and value is the tree.Tree for that user name.
	// Access to userTrees must be protected by the user lock. Get the
	// user lock by calling userLock(userName) and take it prior to getting
	// a Tree from the userTree and while using the Tree. Concurrent access
	// for different users is okay as the LRU is thread-safe.
	userTrees *cache.LRU

	// access caches the parsed contents of Access files, indexed by their
	// path names.
	access *cache.LRU

	// defaultAccess is the empty Access file that implicitly exists at the
	// root if one is not found.
	defaultAccess *access.Access
}

var _ upspin.DirServer = (*server)(nil)

// New creates a new instance of DirServer with the given options
func New(context upspin.Context, options ...string) (upspin.DirServer, error) {
	const op = "DirServer.New"
	if context == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("need a context"))
	}
	if context.DirEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("directory endpoint cannot be unassigned"))
	}
	if context.KeyEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("key endpoint cannot be unassigned"))
	}
	if context.StoreEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("store endpoint cannot be unassigned"))
	}
	if context.UserName() == "" {
		return nil, errors.E(op, errors.Invalid, errors.Str("dirserver needs a user name"))
	}
	if context.Factotum() == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("dirserver requires a factotum"))
	}
	// Check which options are present and pick suitable defaults.
	userCacheSize := 1000
	accessCacheSize := 1000
	logDir := ""
	for i := 0; i < len(options); i += 2 {
		opt := options[i]
		switch opt {
		case "userCacheSize", "accessCacheSize":
			cacheSize, err := strconv.ParseInt(options[i+1], 10, 32)
			if err != nil {
				return nil, errors.E(op, errors.Invalid, err)
			}
			if cacheSize < 1 {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("%s: cache size too small: %d", opt, cacheSize))
			}
			switch opt {
			case "userCacheSize":
				userCacheSize = int(cacheSize)
			case "accessCacheSize":
				accessCacheSize = int(cacheSize)
			}
		case "logDir":
			logDir = options[i+1]
		default:
			return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown option %s", opt))
		}
	}
	if logDir == "" {
		dir, err := ioutil.TempDir("", "DirServer")
		if err != nil {
			return nil, errors.E(op, errors.IO, err)
		}
		log.Error.Printf("Warning: writing important logs to a temp dir (%q). A server restart will lose data.", dir)
		logDir = dir
	}

	return &server{
		serverContext: context,
		logDir:        logDir,
		userTrees:     cache.NewLRU(userCacheSize),
		access:        cache.NewLRU(accessCacheSize),
	}, nil
}

// Lookup implements upspin.DirServer.
func (s *server) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "DirServer.Lookup"
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}
	lock := userLock(p.User())
	lock.Lock()
	defer lock.Unlock()

	// Check access permission.
	canRead, link, err := s.hasRight(access.Read, p)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		return nil, errors.E(op, name, access.ErrPermissionDenied)
	}

	return s.lookup(op, p, entryMustBeClean)
}

// lookup implements Lookup for a parsed path. It is used by Lookup as well as
// by put. If entryMustBeClean is true, the returned entry is guaranteed to have
// valid references in its DirBlocks.
// userLock must be held for p.User().
func (s *server) lookup(op string, p path.Parsed, entryMustBeClean bool) (*upspin.DirEntry, error) {
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, dirty, err := tree.Lookup(p.Path())
	if err != nil {
		// This could be ErrFollowLink so return the entry as well.
		return entry, err
	}
	if dirty && entryMustBeClean {
		// Flush and repeat.
		err = tree.Flush()
		if err != nil {
			return nil, errors.E(op, err)
		}
		entry, dirty, err = tree.Lookup(p.Path())
		if err != nil {
			return nil, errors.E(op, err)
		}
		if dirty {
			return nil, errors.E(op, errors.Internal, errors.Str("flush didn't clean entry"))
		}
	}
	return entry, nil
}

// Put implements upspin.DirServer.
func (s *server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "DirServer.Put"
	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, entry.Name, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	canWrite, link, err := s.hasRight(access.Write, p)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canWrite {
		return nil, errors.E(op, s.userName, p.Path(), access.ErrPermissionDenied)
	}

	// Put is for regular files and links, not directories.
	if entry.IsDir() {
		return nil, errors.E(op, entry.Name, errors.IsDir)
	}

	return s.put(op, p, entry)
}

// MakeDirectory implements upspin.DirServer.
func (s *server) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "DirServer.MakeDirectory"
	p, err := path.Parse(dirName)
	if err != nil {
		return nil, errors.E(op, dirName, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	// Is this the root we're making? Handle it separately.
	if p.IsRoot() {
		return s.createRoot(op, p)
	}

	if access.IsAccessFile(dirName) || access.IsGroupFile(dirName) {
		return nil, errors.E(op, errors.Invalid, errors.Str("cannot make directory named Access or Group"))
	}

	// Check access permissions.
	canCreate, link, err := s.hasRight(access.Create, p)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canCreate {
		return nil, errors.E(op, s.userName, p.Path(), access.ErrPermissionDenied)
	}
	// Create a new dir entry for this new dir.
	de := &upspin.DirEntry{
		Name:     dirName, // not guaranteed canonical yet (put will verify)
		Attr:     upspin.AttrDirectory,
		Writer:   s.userName,
		Packing:  s.serverContext.Packing(),
		Time:     upspin.Now(),
		Sequence: upspin.SeqBase,
	}

	// Attempt to put this new dir entry.
	return s.put(op, p, de)
}

// put implements the common functionality between Put and MakeDirectory.
// userLock must be held for p.User().
func (s *server) put(op string, p path.Parsed, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	if p.Path() != entry.Name {
		return nil, errors.E(op, p.Path(), errors.Str("path name is not clean"))
	}
	err := valid.DirEntry(entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Since dir is not the root, the user must have a tree already.
	// Load it now.
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Check for links along the path.
	existingEntry, err := s.lookup(op, p, !entryMustBeClean)
	if err == upspin.ErrFollowLink {
		return existingEntry, err
	}
	if errors.Match(errNotExist, err) {
		// OK; entry not found as expected.
	} else if err != nil {
		// Some unexpected error happened looking up path. Abort.
		return nil, errors.E(op, err)
	} else {
		// Error is nil therefore path exists. Check if we can overwrite.
		if existingEntry.IsDir() {
			return nil, errors.E(op, p.Path(), errors.IsDir, errors.Str("can't overwrite directory"))
		}
		if entry.IsDir() {
			return nil, errors.E(op, p.Path(), errors.Exist, errors.Str("cannot overwrite file with directory"))
		}
	}

	entry, err = tree.Put(entry)
	if err == upspin.ErrFollowLink {
		return entry, err
	}
	if err != nil {
		return nil, errors.E(op, p.Path(), err)
	}
	return entry, nil
}

// Glob implements upspin.DirServer.
func (s *server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "DirServer.Glob"
	return nil, errors.E(op, errNotImplemented)
}

// Delete implements upspin.DirServer.
func (s *server) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "DirServer.Delete"
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	canDelete, link, err := s.hasRight(access.Delete, p)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canDelete {
		return nil, errors.E(op, name, access.ErrPermissionDenied)
	}
	if p.IsRoot() {
		return nil, errors.E(op, name, errors.Invalid, errors.Str("cannot delete root"))
	}

	// Load the entry so we can check whether it's a dir.
	tree, err := s.loadTreeFor(p.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, _, err := tree.Lookup(p.Path())
	if err != nil {
		// This could be ErrFollowLink so return the entry as well.
		return entry, err
	}

	if entry.IsDir() {
		size, err := entry.Size()
		if err != nil {
			return nil, errors.E(op, err)
		}
		if size > 0 {
			return nil, errors.E(op, errors.NotEmpty)
		}
	}
	return tree.Delete(name)
}

// WhichAccess implements upspin.DirServer.
func (s *server) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "DirServer.WhichAccess"
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	// Check whether the user has Any right on p.
	hasAny, link, err := s.hasRight(access.AnyRight, p)
	if err == upspin.ErrFollowLink {
		// TODO: We may have more work to do. We may need to check
		// whether the user has Any right on the link itself.
		// https://github.com/googleprivate/upspin/issues/39
		return link, err
	}
	if err != nil {
		// TODO: this could leak the existence of name. But the attacker
		// needs to get lucky to trigger an error; a poorly-constructed
		// name is not enough.
		return nil, errors.E(op, err)
	}
	if !hasAny {
		return nil, errors.E(op, errors.NotExist, name)
	}

	return s.whichAccess(p)
}

// Dial implements upspin.Dialer.
func (s *server) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "DirServer.Dial"
	if e.Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("transport must not be unassigned"))
	}
	if err := valid.UserName(ctx.UserName()); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	cp := *s // copy of the generator instance.
	// Override userName and defaultAccess (rest is "global").
	cp.userName = ctx.UserName()
	var err error
	cp.defaultAccess, err = access.New(upspin.PathName(cp.userName + "/"))
	if err != nil {
		return nil, errors.E(op, err)
	}
	return &cp, nil
}

// Configure implements upspin.Service.
func (s *server) Configure(options ...string) error {
	// TODO: to be removed.
	return nil
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	// TODO: to be removed.
	return s.serverContext.DirEndpoint()
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Authenticate implements upspin.Service.
func (s *server) Authenticate(upspin.Context) error {
	// Authentication is handled by higher layers elsewhere.
	return nil
}

// Close implements upspin.Service.
func (s *server) Close() {
	// TODO
}

// loadTreeFor loads the user's tree, if it exists.
// userLock must be held for user.
func (s *server) loadTreeFor(user upspin.UserName) (tree.Tree, error) {
	const op = "loadTreeFor"
	if err := valid.UserName(user); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	// Do we have a cached tree for this user already?
	if val, found := s.userTrees.Get(user); found {
		if tree, ok := val.(tree.Tree); ok {
			return tree, nil
		}
		// This should never happen because we only store type tree.Tree in the userTree.
		return nil, errors.E(op, user, errors.Internal,
			errors.Errorf("userTrees contained value of unexpected type %T", val))
	}
	// User is not in the cache. Load a tree from the logs, if they exist.
	log, logIndex, err := tree.NewLogs(user, s.logDir)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// If user has root, we can load the tree from it.
	if _, err := logIndex.Root(); err != nil {
		// Likely the user has no root yet.
		return nil, errors.E(op, err)
	}
	// Create a new tree for the user.
	tree, err := tree.New(s.serverContext, log, logIndex)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Add to the cache and return
	s.userTrees.Add(user, tree)
	return tree, nil
}

// createRoot creates a new root for a user, if some checks pass.
// userLock must be held for user.
func (s *server) createRoot(op string, p path.Parsed) (*upspin.DirEntry, error) {
	if s.userName != p.User() {
		// Can only create root for the authenticated user.
		return nil, errors.E(op, errors.Invalid, s.userName,
			errors.Str("can't create root for another user"))
	}
	// Is there a tree for such user already?
	_, err := s.loadTreeFor(p.User())
	if err == nil {
		// Can't make root again if tree is found.
		return nil, errors.E(op, errors.Exist, p.Path())
	}
	if !errors.Match(errNotExist, err) {
		// Some other error loading tree. Abort.
		return nil, errors.E(op, p.Path(), err)
	}
	log.Debug.Printf("Creating new logs for user: %q", p.User())

	// Create logs first.
	logger, logIndex, err := tree.NewLogs(p.User(), s.logDir)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Initialize the logIndex so we're at the end of the new log.
	err = logIndex.SaveOffset(0)
	if err != nil {
		return nil, errors.E(op, err)
	}
	log.Debug.Printf("Creating new tree for user: %q", p.User())

	// Create a new tree for the user.
	tree, err := tree.New(s.serverContext, logger, logIndex)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Create a new dir entry for this new dir.
	de := &upspin.DirEntry{
		Name:     p.Path(),
		Attr:     upspin.AttrDirectory,
		Writer:   s.userName,
		Packing:  s.serverContext.Packing(),
		Time:     upspin.Now(),
		Sequence: upspin.SeqBase,
	}

	// Attempt to put this new dir entry as the root.
	_, err = tree.Put(de)
	if err == upspin.ErrFollowLink {
		// The root can't be a link. Something very bad happened.
		return nil, errors.E(op, errors.Internal, p.User(), p.Path(), errors.Str("got ErrFollowLink putting root"))
	}
	if err != nil {
		// This can't be a Link redirection (roots can't be links).
		return nil, errors.E(op, err)
	}

	// Add to the cache and return.
	s.userTrees.Add(p.User(), tree)

	log.Info.Printf("Created root for user %q", p.User())
	return de, nil
}
