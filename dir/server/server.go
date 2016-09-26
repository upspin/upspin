// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements DirServer using a Tree as backing.
package server

import (
	"io/ioutil"
	goPath "path"
	"strconv"
	"strings"

	"upspin.io/access"
	"upspin.io/cache"
	"upspin.io/dir/server/tree"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/valid"
)

// common error values.
var (
	errNotExist = errors.E(errors.NotExist)
	errReadOnly = errors.E(errors.Permission, errors.Str("tree is read only"))
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

	// defaultAccess caches parsed empty Access files that implicitly exists
	// at the root of every user's tree, if an explicit one is not found.
	// It's indexed by the username.
	defaultAccess *cache.LRU

	// stopSnapshot is a channel for shutting down the snapshot loop.
	stopSnapshot chan bool

	// now returns the time now. It's usually just upspin.Now but is
	// overridden for tests.
	now func() upspin.Time
}

var _ upspin.DirServer = (*server)(nil)

// options are optional parameters to almost every inner method of directory
// for doing optional, non-correctness-related work.
type options struct {
	span *metric.Span
	// Add other things below (for example, some health monitoring stats).
}

// New creates a new instance of DirServer with the given options
func New(ctxt upspin.Context, options ...string) (upspin.DirServer, error) {
	const op = "dir/server.New"
	if ctxt == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("nil context"))
	}
	if ctxt.DirEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("directory endpoint cannot be unassigned"))
	}
	if ctxt.KeyEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("key endpoint cannot be unassigned"))
	}
	if ctxt.StoreEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("store endpoint cannot be unassigned"))
	}
	if ctxt.UserName() == "" {
		return nil, errors.E(op, errors.Invalid, errors.Str("empty user name"))
	}
	if ctxt.Factotum() == nil {
		return nil, errors.E(op, errors.Invalid, errors.Str("nil factotum"))
	}
	// Check which options are present and pick suitable defaults.
	userCacheSize := 1000
	accessCacheSize := 1000
	logDir := ""
	for _, opt := range options {
		o := strings.Split(opt, "=")
		if len(o) != 2 {
			return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid option format: %q", opt))
		}
		k, v := o[0], o[1]
		switch k {
		case "userCacheSize", "accessCacheSize":
			cacheSize, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid cache size %q: %s", v, err))
			}
			if cacheSize < 1 {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("%s: cache size too small: %d", k, cacheSize))
			}
			switch opt {
			case "userCacheSize":
				userCacheSize = int(cacheSize)
			case "accessCacheSize":
				accessCacheSize = int(cacheSize)
			}
		case "logDir":
			logDir = v
		default:
			return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown option %q", k))
		}
	}
	if logDir == "" {
		dir, err := ioutil.TempDir("", "DirServer")
		if err != nil {
			return nil, errors.E(op, errors.IO, err)
		}
		log.Error.Printf("Warning: writing important logs to a temporary directory (%q). A server restart will lose data.", dir)
		logDir = dir
	}

	s := &server{
		serverContext: ctxt,
		userName:      ctxt.UserName(),
		logDir:        logDir,
		userTrees:     cache.NewLRU(userCacheSize),
		access:        cache.NewLRU(accessCacheSize),
		defaultAccess: cache.NewLRU(accessCacheSize),
		now:           upspin.Now,
	}
	s.startSnapshotLoop()
	return s, nil
}

// Lookup implements upspin.DirServer.
func (s *server) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/server.Lookup"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	if isSnapshotUser(p.User()) {
		if isSnapshotOwner(s.userName, p.User()) {
			return s.lookup(op, p, entryMustBeClean, o)
		}
		// Non-owners cannot see other people's snapshots.
		return nil, errors.E(op, name, errNotExist)
	}

	entry, err := s.lookup(op, p, entryMustBeClean, o)

	log.Printf("Looking up %q for user %q", name, s.userName)

	// Check if the user can know about the file at all. If not, to prevent
	// leaking its existence, return NotExist.
	if err == upspin.ErrFollowLink {
		return s.errLink(op, entry, o)
	}
	if err != nil {
		return nil, err // s.lookup wraps err already.
	}

	// Check for Read access permission.
	canRead, _, err := s.hasRight(access.Read, p, o)
	if err == upspin.ErrFollowLink {
		return nil, errors.E(op, errors.Internal, p.Path(), errors.Str("can't be link at this point"))
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		log.Printf("can't read %q", p.Path())
		return nil, s.errPerm(op, p, o)
	}
	log.Printf("entry for %q: %+v", p.Path(), entry)
	return entry, nil
}

// lookup implements Lookup for a parsed path. It is used by Lookup as well as
// by put. If entryMustBeClean is true, the returned entry is guaranteed to have
// valid references in its DirBlocks.
// userLock must be held for p.User().
func (s *server) lookup(op string, p path.Parsed, entryMustBeClean bool, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("lookup", opts)
	defer ss.End()

	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, dirty, err := tree.Lookup(p)
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
		entry, dirty, err = tree.Lookup(p)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if dirty {
			return nil, errors.E(op, errors.Internal, errors.Str("flush didn't clean entry"))
		}
	}
	if entry.IsLink() {
		return entry, upspin.ErrFollowLink
	}
	return entry, nil
}

// Put implements upspin.DirServer.
func (s *server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/server.Put"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, entry.Name, err)
	}
	if isSnapshotUser(p.User()) {
		if !isSnapshotOwner(s.userName, p.User()) {
			// Non-owners can't even see the snapshot.
			return nil, errors.E(op, entry.Name, errNotExist)
		}
		if !p.IsRoot() {
			// Not root: owner can't mutate anything else.
			return nil, errors.E(op, entry.Name, errReadOnly)
		}
		// Else: isOwner && putting the root -> OK.
	}

	isAccessOrGroup := access.IsAccessFile(p.Path()) || access.IsGroupFile(p.Path())

	// Links can't be named Access or Group and must use only Plain pack.
	if entry.IsLink() {
		if isAccessOrGroup {
			return nil, errors.E(op, p.Path(), errors.Invalid, errors.Str("link cannot be named Access or Group"))
		}
		if entry.Packing != upspin.PlainPack {
			return nil, errors.E(op, p.Path(), errors.Invalid, errors.Str("link can only use PlainPack"))
		}
	}
	// Directories cannot have reserved names.
	if entry.IsDir() && isAccessOrGroup {
		return nil, errors.E(op, errors.Invalid, entry.Name, errors.Str("cannot make directory named Access or Group"))
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	return s.put(op, p, entry, o)
}

// put implements the bulk of Put.
// userLock must be held for p.User().
func (s *server) put(op string, p path.Parsed, entry *upspin.DirEntry, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("put", opts)
	defer ss.End()

	if p.Path() != entry.Name {
		return nil, errors.E(op, p.Path(), errors.Str("path name is not clean"))
	}
	err := valid.DirEntry(entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Check for links along the path.
	existingEntry, err := s.lookup(op, p, !entryMustBeClean, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, existingEntry, o)
	}

	if errors.Match(errNotExist, err) {
		// OK; entry not found as expected. Can we create it?
		canCreate, _, err := s.hasRight(access.Create, p, o)
		if err == upspin.ErrFollowLink {
			return nil, errors.E(op, p.Path(), errors.Internal, errors.Str("unexpected ErrFollowLink"))
		}
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !canCreate {
			return nil, s.errPerm(op, p, o)
		}
		// New file should have a valid sequence number, if user didn't pick one already.
		if entry.Sequence == upspin.SeqNotExist || entry.Sequence == upspin.SeqIgnore && !entry.IsDir() {
			entry.Sequence = upspin.SeqBase
		}
	} else if err != nil {
		// Some unexpected error happened looking up path. Abort.
		return nil, errors.E(op, err)
	} else {
		// Error is nil therefore path exists.
		// Check if it's root.
		if p.IsRoot() {
			return nil, errors.E(op, p.Path(), errors.Exist)
		}
		// Check if we can overwrite.
		if existingEntry.IsDir() {
			return nil, errors.E(op, p.Path(), errors.Exist, errors.Str("can't overwrite directory"))
		}
		if entry.IsDir() {
			return nil, errors.E(op, p.Path(), errors.Exist, errors.Str("can't overwrite file with directory"))
		}
		// To overwrite a file, we need Write permission.
		canWrite, _, err := s.hasRight(access.Write, p, o)
		if err == upspin.ErrFollowLink {
			return nil, errors.E(op, p.Path(), errors.Internal, errors.Str("unexpected ErrFollowLink"))
		}
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !canWrite {
			return nil, s.errPerm(op, p, o)
		}
		// If the file is expected not to be there, it's an error.
		if entry.Sequence == upspin.SeqNotExist {
			return nil, errors.E(op, entry.Name, errors.Exist)
		}
		// We also must have the correct sequence number or SeqIgnore.
		if entry.Sequence != upspin.SeqIgnore {
			if entry.Sequence != existingEntry.Sequence {
				return nil, errors.E(op, entry.Name, errors.Invalid, errors.Str("sequence number"))
			}
		}
		// Note: sequence number updates for directories is maintained
		// by the Tree since directory entries are never Put by the
		// user explicitly. Here we adjust the dir entries that the user
		// sent us (those representing files only).
		entry.Sequence = existingEntry.Sequence + 1

		// If we're updating an Access file, remove the old one from the
		// accessCache. Let the new one be loaded lazily.
		if access.IsAccessFile(entry.Name) {
			s.access.Remove(entry.Name)
		}
		// If we're updating a Group file, remove the old one from the
		// access group cache. Let the new one be loaded lazily.
		if access.IsGroupFile(entry.Name) {
			err = access.RemoveGroup(entry.Name)
			if err != nil {
				// Nothing to do but log.
				log.Error.Printf("%s: Error removing group file: %s", op, err)
			}
		}
	}

	entry, err = tree.Put(p, entry)
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
	const op = "dir/server.Glob"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, errors.E(op, err)
	}

	overrideAccessCheck := false
	if isSnapshotUser(p.User()) {
		if isSnapshotOwner(s.userName, p.User()) {
			// Owners can glob everything, regardless of the
			// original Access files.
			overrideAccessCheck = true
		} else {
			// Non-owners can't see anything.
			return nil, errors.E(op, p.Path(), errNotExist)
		}
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	return s.glob(op, p, overrideAccessCheck, o)
}

// glob implements the bulk of Glob on a pattern p, optionally allowing for
// Access checks to be overriden.
// userLock for pattern.User() must be held.
func (s *server) glob(op string, p path.Parsed, overrideAccessCheck bool, opts ...options) ([]*upspin.DirEntry, error) {
	tree, err := s.loadTreeFor(p.User(), opts...)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// User wants valid dir entries, so we must flush the Tree (we could
	// check if !dirty first, but flush when nothing is dirty is cheap and
	// doing everything again if it was dirty is expensive, so flush now).
	err = tree.Flush()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Look for the longest prefix that does not contain a metacharacter, so
	// we know which level we need to apply Glob and where to start looking
	// for Access files. If no metacharacter exists, start globbing from the
	// parent dir of the target.
	firstMeta := p.NElem() - 1
	if firstMeta < 0 {
		firstMeta = 0 // For root, p.NElem is zero, so adjust here.
	}
	for i := 0; i < p.NElem(); i++ {
		if strings.ContainsAny(p.Elem(i), "*?[]^") {
			firstMeta = i
			break
		}
	}

	var errFollowLink error
	var entries []*upspin.DirEntry
	toList := []path.Parsed{p.First(firstMeta)}
	i := 0 // i is the iterator over toList. It only moves forward.
	for d := firstMeta; d < p.NElem(); d++ {
		for ; i < len(toList); i++ { // not range loop, slice grows.
			dir := toList[i]
			if dir.NElem() > d {
				// We've listed all dirs at this level. Move to
				// next level (move d forward).
				break
			}
			canList, _, err := s.hasRight(access.List, dir, opts...)
			if err != nil && !errors.Match(errNotExist, err) {
				return nil, errors.E(op, err)
			}
			canList = canList || overrideAccessCheck
			if !canList {
				if d == firstMeta {
					return nil, s.errPerm(op, p.First(d))
				}
				continue
			}
			ents, _, err := tree.List(dir)
			if err != nil {
				return nil, errors.E(op, err)
			}
			// Apply goPath regexp to each e in ents and verify
			// access rights.
			for _, e := range ents {
				// It's safe to request d+1 because we just listed a directory at level +1 from current.
				matched, err := goPath.Match(p.First(d+1).String(), string(e.Name))
				if err != nil {
					return nil, errors.E(op, p.Path(), errors.Invalid, err)
				}
				if !matched {
					continue
				}
				// Next, we must list any subdirs, unless the pattern is finished.
				if d == p.NElem()-1 {
					// If we can't read, strip Packdata and Location information.
					canRead, _, err := s.hasRight(access.Read, dir, opts...)
					if err != nil && !errors.Match(errNotExist, err) {
						return nil, errors.E(op, err)
					}
					canRead = canRead || overrideAccessCheck
					if canRead {
						entries = append(entries, e)
					} else {
						// Make a shallow copy, since we need to clean
						// the entry.
						eCopy := *e
						eCopy.Packdata = nil
						eCopy.Blocks = nil
						entries = append(entries, &eCopy)
					}
				} else {
					// A link is always added, even if matched partially.
					if e.IsLink() {
						errFollowLink = upspin.ErrFollowLink
						entries = append(entries, e)
						continue
					}
					// Pattern not finished. Add subdirs.
					if e.IsDir() {
						// e.Name is known valid.
						p, _ := path.Parse(e.Name)
						toList = append(toList, p)
					}
				}
			}
		}
	}

	upspin.SortDirEntries(entries, false)
	return entries, errFollowLink
}

// Delete implements upspin.DirServer.
func (s *server) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/server.Delete"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}
	if isSnapshotUser(p.User()) {
		if isSnapshotOwner(s.userName, p.User()) {
			// Owner can't mutate.
			return nil, errors.E(op, name, errReadOnly)
		}
		// Everyone else can't even see it.
		return nil, errors.E(op, name, errNotExist)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	canDelete, link, err := s.hasRight(access.Delete, p, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, link, o)
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canDelete {
		return nil, errors.E(op, name, access.ErrPermissionDenied)
	}

	// Load the tree for this user.
	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := tree.Delete(p)
	if err != nil {
		return entry, err // could be ErrFollowLink.
	}
	// If we just deleted an Access file, remove it from the accessCache too.
	if access.IsAccessFile(p.Path()) {
		s.access.Remove(p.Path())
	}
	// If we just deleted a Group file, remove it from the Group cache too.
	if access.IsGroupFile(p.Path()) {
		err = access.RemoveGroup(p.Path())
		if err != nil {
			// Nothing to do but log.
			log.Error.Printf("%s: Error removing group file: %s", op, err)
		}
	}

	return entry, nil
}

// WhichAccess implements upspin.DirServer.
func (s *server) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/server.WhichAccess"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	// Check whether the user has Any right on p.
	hasAny, link, err := s.hasRight(access.AnyRight, p, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, link, o)
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

	return s.whichAccess(p, o)
}

// Dial implements upspin.Dialer.
func (s *server) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "dir/server.Dial"
	if e.Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("transport must not be unassigned"))
	}
	if err := valid.UserName(ctx.UserName()); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	cp := *s // copy of the generator instance.
	// Override userName (rest is "global").
	cp.userName = ctx.UserName()
	// create a default Access file for this user and cache it.
	defaultAccess, err := access.New(upspin.PathName(cp.userName + "/"))
	if err != nil {
		return nil, errors.E(op, err)
	}
	cp.defaultAccess.Add(cp.userName, defaultAccess)
	return &cp, nil
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

// Close implements upspin.Service.
func (s *server) Close() {
	const op = "dir/server.Close"

	// Remove this user's tree from the cache. This allows it to be
	// garbage-collected even if other servers have pointers into the
	// cache (which at least one will have, the one created with New).

	mu := userLock(s.userName)
	mu.Lock()
	defer mu.Unlock()

	t := s.userTrees.Remove(s.userName)
	if tree, ok := t.(*tree.Tree); ok {
		// Flush everything since Remove won't invoke EvictionNotifier.
		err := tree.Flush()
		if err != nil {
			// TODO: return an error when Close expects it.
			log.Error.Printf("%s: Error flushing user tree %q: %q", op, s.userName, err)
		}
	}

	s.defaultAccess = nil
	s.stopSnapshotLoop()
}

// loadTreeFor loads the user's tree, if it exists.
// userLock must be held for user.
func (s *server) loadTreeFor(user upspin.UserName, opts ...options) (*tree.Tree, error) {
	defer span(opts).StartSpan("loadTreeFor").End()

	if err := valid.UserName(user); err != nil {
		return nil, errors.E(errors.Invalid, err)
	}

	// Do we have a cached tree for this user already?
	if val, found := s.userTrees.Get(user); found {
		if tree, ok := val.(*tree.Tree); ok {
			return tree, nil
		}
		// This should never happen because we only store type tree.Tree in the userTree.
		return nil, errors.E(user, errors.Internal,
			errors.Errorf("userTrees contained value of unexpected type %T", val))
	}
	// User is not in the cache. Load a tree from the logs, if they exist.
	hasLog, err := tree.HasLog(user, s.logDir)
	if err != nil {
		return nil, err
	}
	if !hasLog && s.userName != user {
		// Tree for user does not exist and the logged-in user is not
		// allowed to create it.
		return nil, errNotExist
	}
	log, logIndex, err := tree.NewLogs(user, s.logDir)
	if err != nil {
		return nil, err
	}
	// If user has root, we can load the tree from it.
	if _, err := logIndex.Root(); err != nil {
		// Likely the user has no root yet.
		if !errors.Match(errNotExist, err) {
			// No it's some other error. Abort.
			return nil, err
		}
		// Ok, let it proceed. The  user will still need to make the
		// root, but we allow setting up a new tree for now.
		err = logIndex.SaveOffset(0)
		if err != nil {
			return nil, err
		}
		// Fall through and load a new tree.
	}
	// Create a new tree for the user.
	tree, err := tree.New(s.serverContext, log, logIndex)
	if err != nil {
		return nil, err
	}
	// Add to the cache and return
	s.userTrees.Add(user, tree)
	return tree, nil
}

// errPerm checks whether the user has any right to the given path, and if so
// returns a Permission error. Otherwise it returns a NotExist error.
// This is used to prevent probing of the name space.
func (s *server) errPerm(op string, p path.Parsed, opts ...options) error {
	// Before returning, check that the user has the right to know,
	// to prevent leaking the name space.
	if hasAny, _, err := s.hasRight(access.AnyRight, p, opts...); err != nil {
		// Some error other than ErrFollowLink.
		return errors.E(op, err)
	} else if !hasAny {
		// User does not have Any right. Pretend it doesn't exist.
		return errors.E(op, p.Path(), errors.NotExist)
	}
	return errors.E(op, p.Path(), access.ErrPermissionDenied)
}

// errLink checks whether the user has any right to the given entry, and if so
// returns the entry and ErrFollowLink. If the use has no rights, it returns a
// NotExist error. This is used to prevent probing of the name space using
// links.
func (s *server) errLink(op string, link *upspin.DirEntry, opts ...options) (*upspin.DirEntry, error) {
	p, err := path.Parse(link.Name)
	if err != nil {
		return nil, errors.E(op, errors.Internal, link.Name, err)
	}
	if hasAny, _, err := s.hasRight(access.AnyRight, p, opts...); err != nil {
		// Some error other than ErrFollowLink.
		return nil, errors.E(op, err)
	} else if hasAny {
		// User has Any right on the link. Let them follow it.
		return link, upspin.ErrFollowLink
	}
	// Denied. User has no right on link. Pretend it doesn't exist.
	return nil, errors.E(op, p.Path(), errors.NotExist)
}

// newOptMetric creates a new options populated with a metric for operation op.
func newOptMetric(op string) (options, *metric.Metric) {
	m := metric.New("server")
	opts := options{
		span: m.StartSpan(op),
	}
	return opts, m
}

// span returns the first span found in opts or a new one if not found.
func span(opts []options) *metric.Span {
	for _, o := range opts {
		if o.span != nil {
			return o.span
		}
	}
	// This is probably an error. Metrics should be created at the entry
	// points only.
	return metric.New("FIXME").StartSpan("FIXME")
}

// subspan creates a span for an operation op in the given option. It returns
// a new option with the new span, for passing along subfunctions.
func subspan(op string, opts []options) (options, *metric.Span) {
	s := span(opts).StartSpan(op)
	return options{span: s}, s
}
