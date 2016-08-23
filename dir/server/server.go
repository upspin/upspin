// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements DirServer using a Tree as backing.
package server

import (
	"io/ioutil"
	goPath "path"
	"sort"
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

// errNotExist is only used for comparison, to detect whether entries already
// exist.
var errNotExist = errors.E(errors.NotExist)

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
			return nil, errors.E(op, errors.Syntax, errors.Errorf("invalid option format: %q", opt))
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

	return &server{
		serverContext: ctxt,
		logDir:        logDir,
		userTrees:     cache.NewLRU(userCacheSize),
		access:        cache.NewLRU(accessCacheSize),
	}, nil
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
	lock := userLock(p.User())
	lock.Lock()
	defer lock.Unlock()

	// Check access permission.
	canRead, link, err := s.hasRight(access.Read, p, o)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		return nil, errors.E(op, name, access.ErrPermissionDenied)
	}

	return s.lookup(op, p, entryMustBeClean, o)
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

	// Links can't be named Access or Group and must use only Plain pack.
	if entry.IsLink() {
		if access.IsAccessFile(p.Path()) || access.IsGroupFile(p.Path()) {
			return nil, errors.E(op, p.Path(), errors.Invalid, errors.Str("link cannot be named Access or Group"))
		}
		if entry.Packing != upspin.PlainPack {
			return nil, errors.E(op, p.Path(), errors.Invalid, errors.Str("link can only use PlainPack"))
		}
	}
	// Put is for regular files and links, not directories.
	if entry.IsDir() {
		return nil, errors.E(op, entry.Name, errors.IsDir)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	// First round of access checks: can user write or create? If not,
	// reject early.
	canWrite, link, err := s.hasRight(access.Write, p, o)
	if err == upspin.ErrFollowLink {
		return link, err
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	canCreate, _, err := s.hasRight(access.Create, p, o) // ErrFollowLink won't happen here.
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canWrite && !canCreate {
		return nil, errors.E(op, s.userName, p.Path(), access.ErrPermissionDenied)
	}

	return s.put(op, p, entry, canCreate, canWrite, o)
}

// MakeDirectory implements upspin.DirServer.
func (s *server) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/server.MakeDirectory"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(dirName)
	if err != nil {
		return nil, errors.E(op, dirName, err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	// Is this the root we're making? Handle it separately.
	if p.IsRoot() {
		return s.createRoot(op, p, o)
	}

	if access.IsAccessFile(dirName) || access.IsGroupFile(dirName) {
		return nil, errors.E(op, errors.Invalid, errors.Str("cannot make directory named Access or Group"))
	}

	// Check access permissions.
	canCreate, link, err := s.hasRight(access.Create, p, o)
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
	const canWrite = true

	// Attempt to put this new dir entry. We know we canCreate & !canWrite.
	return s.put(op, p, de, canCreate, !canWrite, o)
}

// put implements the common functionality between Put and MakeDirectory.
// userLock must be held for p.User().
func (s *server) put(op string, p path.Parsed, entry *upspin.DirEntry, canCreate, canWrite bool, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("put", opts)
	defer ss.End()

	if p.Path() != entry.Name {
		return nil, errors.E(op, p.Path(), errors.Str("path name is not clean"))
	}
	err := valid.DirEntry(entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Since dir is not the root, the user must have a tree already.
	// Load it now.
	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Check for links along the path.
	existingEntry, err := s.lookup(op, p, !entryMustBeClean, o)
	if err == upspin.ErrFollowLink {
		return existingEntry, err
	}

	if errors.Match(errNotExist, err) {
		// OK; entry not found as expected. Can we create it?
		if !canCreate {
			return nil, errors.E(op, p.Path(), access.ErrPermissionDenied)
		}
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
		// To overwrite a file, we need Write permission.
		if !canWrite {
			return nil, errors.E(op, p.Path(), access.ErrPermissionDenied)
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

	// Check if pattern is a valid Go path pattern.
	_, err = goPath.Match(string(p.FilePath()), "")
	if err != nil {
		return nil, errors.E(op, p.Path(), err)
	}

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	tree, err := s.loadTreeFor(p.User(), o)
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
	// for Access files.
	firstMeta := p.NElem()
	for i := 0; i < firstMeta; i++ {
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
			canList, _, err := s.hasRight(access.List, dir, o)
			if err != nil {
				return nil, errors.E(op, err)
			}
			if !canList {
				continue
			}
			ents, _, err := tree.List(dir)
			if err != nil {
				return nil, errors.E(op, err)
			}
			// Apply goPath regexp to each e in ents and verify
			// access rights.
			for _, e := range ents {
				// No need to check for errors, pattern was validated above.
				// It's safe to request d+1 because we just listed a directory at level +1 from current.
				if matched, _ := goPath.Match(p.First(d+1).String(), string(e.Name)); !matched {
					continue
				}
				// Next, we must list any subdirs, unless the pattern is finished.
				if d == p.NElem()-1 {
					// If we can't read, strip Packdata and Location information.
					canRead, _, err := s.hasRight(access.Read, dir, o)
					if err != nil {
						return nil, errors.E(op, err)
					}
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

	// Sort entries.
	sort.Sort(dirEntrySlice(entries))
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

	mu := userLock(p.User())
	mu.Lock()
	defer mu.Unlock()

	canDelete, link, err := s.hasRight(access.Delete, p, o)
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
		// TODO: support this soon.
		return nil, errors.E(op, name, errors.Invalid, errors.Str("cannot delete root"))
	}

	// Load the entry so we can check whether it's a dir.
	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, _, err := tree.Lookup(p)
	if err != nil {
		// This could be ErrFollowLink so return the entry as well.
		return entry, err
	}
	return tree.Delete(p)
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
func (s *server) Configure(options ...string) (upspin.UserName, error) {
	// TODO: to be removed.
	return "", nil
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
func (s *server) loadTreeFor(user upspin.UserName, opts ...options) (*tree.Tree, error) {
	const op = "dir/server.loadTreeFor"
	defer span(opts).StartSpan(op).End()

	if err := valid.UserName(user); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	// Do we have a cached tree for this user already?
	if val, found := s.userTrees.Get(user); found {
		if tree, ok := val.(*tree.Tree); ok {
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
func (s *server) createRoot(op string, p path.Parsed, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("createRoot", opts)
	defer ss.End()

	if s.userName != p.User() {
		// Can only create root for the authenticated user.
		return nil, errors.E(op, errors.Invalid, s.userName,
			errors.Str("can't create root for another user"))
	}
	// Is there a tree for such user already?
	_, err := s.loadTreeFor(p.User(), o)
	if err == nil {
		// Can't make root again if tree is found.
		return nil, errors.E(op, errors.Exist, p.Path())
	}
	if !errors.Match(errNotExist, err) {
		// Some other error loading tree. Abort.
		return nil, errors.E(op, p.Path(), err)
	}

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
	_, err = tree.Put(p, de)
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

// For sorting (copied from dir/inprocess/directory.go).
type dirEntrySlice []*upspin.DirEntry

func (d dirEntrySlice) Len() int           { return len(d) }
func (d dirEntrySlice) Less(i, j int) bool { return d[i].Name < d[j].Name }
func (d dirEntrySlice) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }
