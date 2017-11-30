// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements DirServer using a Tree as backing.
package server

import (
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/cache"
	"upspin.io/cloud/storage"
	"upspin.io/dir/server/serverlog"
	"upspin.io/dir/server/tree"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/serverutil"
	"upspin.io/shutdown"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"
)

// common error values.
var (
	errNotExist = errors.E(errors.NotExist)
	errPrivate  = errors.E(errors.Private)
)

const (
	// entryMustBeClean is used with lookup to specify whether the caller
	// needs to look at the dir entry's references and therefore whether the
	// tree must be flushed if a dirty dir entry is found.
	entryMustBeClean = true
)

// server implements upspin.DirServer.
type server struct {
	// serverConfig holds this server's Factotum, server name and store
	// endpoint where to store dir entries. It is set when the server is
	// first registered and never reset again.
	serverConfig upspin.Config

	// userName is the name of the user on behalf of whom this
	// server is serving.
	userName upspin.UserName

	// baseUser, suffix and domain are the components of userName as parsed
	// by user.Parse.
	userBase, userSuffix, userDomain string

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

	// access caches the parsed contents of Access files as struct
	// accessEntry, indexed by their path names.
	access *cache.LRU

	// defaultAccess caches parsed empty Access files that implicitly exists
	// at the root of every user's tree, if an explicit one is not found.
	// It's indexed by the username.
	defaultAccess *cache.LRU

	// remoteGroups caches groupEntry objects that store remote Group files
	// that must be periodically forgotten so they're reloaded fresh again
	// when needed.
	remoteGroups *cache.LRU

	// userLocks is a pool of user locks. To find the correct lock for a
	// user, a string hash of a username selects the index into the slice to
	// use. This fixed pool ensures we don't have a growing number of locks
	// and that we also don't have a race creating new locks when we first
	// touch a user.
	userLocks []sync.Mutex

	// snapshotControl is a channel for passing control messages to the
	// snapshot loop.
	snapshotControl chan snapshotCreate

	// now returns the time now. It's usually just upspin.Now but is
	// overridden for tests.
	now func() upspin.Time

	// dialed reports whether the instance was created using Dial, not New.
	dialed bool

	// The Storage backend in which to make backup copies of roots.
	// If nil, no backups are made.
	storage storage.Storage
}

// snapshotCreate is used to create a snapshot and report its success.
type snapshotCreate struct {
	userName upspin.UserName
	created  chan error
}

var _ upspin.DirServer = (*server)(nil)

// options are optional parameters to almost every inner method of directory
// for doing optional, non-correctness-related work.
type options struct {
	span *metric.Span
	// Add other things below (for example, some health monitoring stats).
}

// New creates a new instance of DirServer with the given options
func New(cfg upspin.Config, options ...string) (upspin.DirServer, error) {
	const op errors.Op = "dir/server.New"
	if cfg == nil {
		return nil, errors.E(op, errors.Invalid, "nil config")
	}
	if cfg.DirEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "directory endpoint cannot be unassigned")
	}
	if cfg.KeyEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "key endpoint cannot be unassigned")
	}
	if cfg.StoreEndpoint().Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "store endpoint cannot be unassigned")
	}
	if cfg.UserName() == "" {
		return nil, errors.E(op, errors.Invalid, "empty user name")
	}
	if cfg.Factotum() == nil {
		return nil, errors.E(op, errors.Invalid, "nil factotum")
	}
	// Check which options are present and pick suitable defaults.
	var (
		logDir         string
		storageBackend string
		storageOpts    []storage.DialOpts
	)
	for _, opt := range options {
		const logDirPrefix = "logDir="
		if strings.HasPrefix(opt, logDirPrefix) {
			logDir = opt[len(logDirPrefix):]
			continue
		}
		const backendPrefix = "backend="
		if strings.HasPrefix(opt, backendPrefix) {
			storageBackend = opt[len(backendPrefix):]
			continue
		}
		storageOpts = append(storageOpts, storage.WithOptions(opt))
	}
	if logDir == "" {
		dir, err := ioutil.TempDir("", "DirServer")
		if err != nil {
			return nil, errors.E(op, errors.IO, err)
		}
		log.Error.Printf("%s: warning: writing important logs to a temporary directory (%q). A server restart will lose data.", op, dir)
		logDir = dir
	}

	var store storage.Storage
	if storageBackend != "" {
		// Dial a storage backend in which to store the roots.
		var err error
		store, err = storage.Dial(storageBackend, storageOpts...)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}

	const (
		userCacheSize   = 1000
		accessCacheSize = 1000
		groupCacheSize  = 100
	)
	s := &server{
		serverConfig:  cfg,
		userName:      cfg.UserName(),
		logDir:        logDir,
		userTrees:     cache.NewLRU(userCacheSize),
		access:        cache.NewLRU(accessCacheSize),
		defaultAccess: cache.NewLRU(accessCacheSize),
		remoteGroups:  cache.NewLRU(groupCacheSize),
		userLocks:     make([]sync.Mutex, numUserLocks),
		now:           upspin.Now,
		storage:       store,
	}
	shutdown.Handle(s.shutdown)
	// Start background services.
	s.startSnapshotLoop()
	go s.groupRefreshLoop()
	return s, nil
}

// Lookup implements upspin.DirServer.
func (s *server) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/server.Lookup"
	o, m := newOptMetric(op)
	defer m.Done()
	return s.lookupWithPermissions(op, name, o)
}

func (s *server) lookupWithPermissions(op errors.Op, name upspin.PathName, opts ...options) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	entry, err := s.lookup(p, entryMustBeClean, opts...)

	// Check if the user can know about the file at all. If not, to prevent
	// leaking its existence, return Private.
	if err == upspin.ErrFollowLink {
		return s.errLink(op, entry, opts...)
	}
	if err != nil {
		if errors.Is(errors.NotExist, err) {
			if canAny, _, err := s.hasRight(access.AnyRight, p, opts...); err != nil {
				return nil, errors.E(op, err)
			} else if !canAny {
				return nil, errors.E(op, name, errors.Private)
			}
		}
		return nil, errors.E(op, err)
	}

	// Check for Read access permission.
	canRead, _, err := s.hasRight(access.Read, p, opts...)
	if err == upspin.ErrFollowLink {
		return nil, errors.E(op, errors.Internal, p.Path(), "can't be link at this point")
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		canAny, _, err := s.hasRight(access.AnyRight, p, opts...)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !canAny {
			return nil, s.errPerm(op, p, opts...)
		}
		if !access.IsAccessControlFile(entry.SignedName) {
			entry.MarkIncomplete()
		}
	}
	return entry, nil
}

// lookup implements Lookup for a parsed path. It is used by Lookup as well as
// by put. If entryMustBeClean is true, the returned entry is guaranteed to have
// valid references in its DirBlocks.
func (s *server) lookup(p path.Parsed, entryMustBeClean bool, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("lookup", opts)
	defer ss.End()

	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		entry, dirty, err = tree.Lookup(p)
		if err != nil {
			return nil, err
		}
		if dirty {
			return nil, errors.E(errors.Internal, "flush didn't clean entry")
		}
	}
	if entry.IsLink() {
		return entry, upspin.ErrFollowLink
	}
	return entry, nil
}

// Put implements upspin.DirServer.
func (s *server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/server.Put"
	o, m := newOptMetric(op)
	defer m.Done()

	err := valid.DirEntry(entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, entry.Name, err)
	}

	// Special check for the magic file that trigger a snapshot operation.
	// Only the snapshot owner can do it.
	if isSnapshotUser(p.User()) && s.isSnapshotOwner(p.User()) && isSnapshotControlFile(p) {
		err = isValidSnapshotControlEntry(entry)
		if err != nil {
			return nil, errors.E(op, err)
		}
		// Start a snapshot for this user.
		errorC := make(chan error)
		s.snapshotControl <- snapshotCreate{
			userName: p.User(),
			created:  errorC,
		}
		return entry, <-errorC // Returned error reports status of snapshot.
	}

	isAccess := access.IsAccessFile(p.Path())
	isGroup := access.IsGroupFile(p.Path())
	isLink := entry.IsLink()

	// Links can't be named Access or Group.
	if isLink {
		if isAccess || isGroup {
			return nil, errors.E(op, p.Path(), errors.Invalid, "link cannot be named Access or Group")
		}
	}
	// Directories cannot have reserved names.
	if isAccess && entry.IsDir() {
		return nil, errors.E(op, errors.Invalid, entry.Name, "cannot make directory named Access")
	}

	// Special files must use integrity pack (plain text + signature).
	isGroupFile := isGroup && !entry.IsDir()
	if isGroupFile || isAccess {
		packer := pack.Lookup(entry.Packing)
		if packer == nil {
			return nil, errors.E(op, errors.Errorf("unknown packing %s", entry.Packing))
		}
		ok, err := packer.UnpackableByAll(entry)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !ok {
			return nil, errors.E(op, p.Path(), "Access or Group files must be readable by all")
		}
	}

	if isAccess {
		// Validate access files at Put time to detect bad ones early.
		_, err := s.loadAccess(entry, o)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}
	if isGroupFile {
		// Validate group files at Put time to detect bad ones early.
		err = s.loadGroup(p, entry)
		if err != nil {
			return nil, errors.E(op, err)
		}
		// Check that the name is a legal Group name.
		// All elements must satisfy this condition, to protect Access file parsing.
		// TODO: Is this the syntax we should require for any Upspin name?
		for i := 1; i < p.NElem(); i++ { // Element 0 is "Group".
			if _, _, err := user.ParseUser(p.Elem(i)); err != nil {
				return nil, errors.E(op, entry.Name, err)
			}
		}
	}

	// Check for links along the path.
	existingEntry, err := s.lookup(p, !entryMustBeClean, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, existingEntry, o)
	}

	if errors.Is(errors.NotExist, err) {
		// OK; entry not found as expected. Can we create it?
		canCreate, _, err := s.hasRight(access.Create, p, o)
		if err == upspin.ErrFollowLink {
			return nil, errors.E(op, p.Path(), errors.Internal, "unexpected ErrFollowLink")
		}
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !canCreate {
			return nil, s.errPerm(op, p, o)
		}

		// The provided sequence number for a new item may be only SeqNotExist or SeqIgnore.
		if entry.Sequence != upspin.SeqNotExist && entry.Sequence != upspin.SeqIgnore {
			return nil, errors.E(op, p.Path(), errors.Invalid, "invalid sequence number")
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
			return nil, errors.E(op, p.Path(), errors.Exist, "can't overwrite directory")
		}
		if entry.IsDir() {
			return nil, errors.E(op, p.Path(), errors.Exist, "can't overwrite file with directory")
		}
		// To overwrite a file, we need Write permission.
		canWrite, _, err := s.hasRight(access.Write, p, o)
		if err == upspin.ErrFollowLink {
			return nil, errors.E(op, p.Path(), errors.Internal, "unexpected ErrFollowLink")
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
				return nil, errors.E(op, entry.Name, errors.Invalid, "sequence number")
			}
		}

		// If we're updating an Access file delete it from the cache and
		// let it be re-loaded lazily when needed again.
		if access.IsAccessFile(entry.Name) {
			s.access.Remove(entry.Name)
		}
		// If we're updating a Group file, remove the old one from the
		// access group cache. Let the new one be loaded lazily.
		if access.IsGroupFile(entry.Name) {
			err = access.RemoveGroup(entry.Name)
			if err != nil {
				// Nothing to do but log.
				log.Error.Printf("%s: Error removing group file %s: %s", op, entry.Name, err)
			}
		}
	}

	entry, err = s.put(op, p, entry, o)
	if err != nil {
		return entry, err
	}
	// Return Incomplete entry with Sequence number.
	retEntry := &upspin.DirEntry{
		Attr:     upspin.AttrIncomplete,
		Sequence: entry.Sequence,
	}
	return retEntry, nil
}

// put performs Put on the user's tree.
func (s *server) put(op errors.Op, p path.Parsed, entry *upspin.DirEntry, opts ...options) (*upspin.DirEntry, error) {
	o, ss := subspan("put", opts)
	defer ss.End()

	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
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
	const op errors.Op = "dir/server.Glob"
	o, m := newOptMetric(op)
	defer m.Done()

	// lookup implements serverutil.LookupFunc. It checks permissions.
	lookup := func(name upspin.PathName) (*upspin.DirEntry, error) {
		const op errors.Op = "dir/server.Lookup"
		o, ss := subspan(op, []options{o})
		defer ss.End()
		return s.lookupWithPermissions(op, name, o)
	}
	// lookup implements serverutil.ListFunc. It checks permissions.
	listDir := func(dirName upspin.PathName) ([]*upspin.DirEntry, error) {
		const op errors.Op = "dir/server.listDir"
		o, ss := subspan(op, []options{o})
		defer ss.End()
		return s.listDir(op, dirName, o)
	}

	entries, err := serverutil.Glob(pattern, lookup, listDir)
	if err != nil && err != upspin.ErrFollowLink {
		err = errors.E(op, err)
	}
	return entries, err
}

// listDir implements serverutil.ListFunc, with an additional options variadic.
// dirName should always be a directory. It checks permissions.
func (s *server) listDir(op errors.Op, dirName upspin.PathName, opts ...options) ([]*upspin.DirEntry, error) {
	parsed, err := path.Parse(dirName)
	if err != nil {
		return nil, errors.E(op, err)
	}

	tree, err := s.loadTreeFor(parsed.User(), opts...)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Fetch the directory's contents. Don't return the error from List
	// until we know if we have List rights.
	entries, isDirty, listErr := tree.List(parsed)
	if listErr == upspin.ErrFollowLink {
		entry, err := s.errLink(op, entries[0], opts...)
		if entry != nil {
			return []*upspin.DirEntry{entry}, err
		}
		return nil, err
	}

	canList, canRead := false, false
	// Check that we have list rights for any file in the directory.
	canList, _, err = s.hasRight(access.List, parsed, opts...)
	if err != nil {
		// TODO(adg): this error needs sanitizing
		return nil, errors.E(op, dirName, err)
	}
	if !canList {
		return nil, errors.E(op, dirName, errors.Private)
	}
	if listErr != nil {
		return nil, errors.E(op, listErr)
	}
	canRead, _, _ = s.hasRight(access.Read, parsed, opts...)

	if canRead && isDirty {
		// User wants DirEntries with valid blocks, so we must flush
		// the Tree if something is dirty and try again.
		err = tree.Flush()
		if err != nil {
			return nil, errors.E(op, err)
		}
		entries, _, err = tree.List(parsed)
		if err != nil { // Not ErrFollowLink
			return nil, errors.E(op, err)
		}
	}
	if !canRead {
		for _, e := range entries {
			if !access.IsAccessControlFile(e.SignedName) {
				e.MarkIncomplete()
			}
		}
	}
	return entries, nil
}

// Delete implements upspin.DirServer.
func (s *server) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/server.Delete"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	canDelete, link, err := s.hasRight(access.Delete, p, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, link, o)
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canDelete {
		return nil, s.errPerm(op, p, o)
	}

	// Load the tree for this user.
	t, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := t.Delete(p)
	if err != nil {
		return entry, err // could be ErrFollowLink.
	}
	// If we just deleted an Access file, remove it from the access cache
	// too.
	if access.IsAccessFile(p.Path()) {
		s.access.Remove(p.Path())
	}
	// If we just deleted a Group file, remove it from the Group cache too.
	if access.IsGroupFile(p.Path()) {
		err = access.RemoveGroup(p.Path())
		if err != nil {
			// Nothing to do but log (it may not have been loaded
			// yet, so it's not an error).
			log.Printf("%s: Error removing group file: %s", op, err)
		}
	}
	// If we just deleted the root, close the tree, remove it from the cache
	// and delete all logs associated with the tree owner.
	if p.IsRoot() {
		user := t.User()
		if err := s.closeTree(p.User()); err != nil {
			return nil, errors.E(op, name, err)
		}
		if err := user.DeleteLogs(); err != nil {
			return nil, errors.E(op, name, err)
		}
	}

	return entry, nil
}

// WhichAccess implements upspin.DirServer.
func (s *server) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/server.WhichAccess"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	// Check whether the user has Any right on p.
	hasAny, link, err := s.hasRight(access.AnyRight, p, o)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, link, o)
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !hasAny {
		return nil, errors.E(op, errors.Private, name)
	}

	return s.whichAccess(p, o)
}

// Watch implements upspin.DirServer.Watch.
func (s *server) Watch(name upspin.PathName, sequence int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	const op errors.Op = "dir/server.Watch"
	o, m := newOptMetric(op)
	defer m.Done()

	p, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, name, err)
	}

	// Don't permit Watches of snapshot trees.
	// See issue #536.
	if isSnapshotUser(p.User()) {
		return nil, upspin.ErrNotSupported
	}

	tree, err := s.loadTreeFor(p.User(), o)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Establish a channel with the tree and start a goroutine that filters
	// out requests not visible by the caller.
	treeEvents, err := tree.Watch(p, sequence, done)
	if err != nil {
		return nil, errors.E(op, err)
	}
	events := make(chan upspin.Event, 1)

	go s.watch(op, treeEvents, events)

	return events, nil
}

// watcher runs in a goroutine reading events from the tree and passing them
// along to the original caller, but first verifying whether the user has rights
// to know about the event.
func (s *server) watch(op errors.Op, treeEvents <-chan *upspin.Event, outEvents chan<- upspin.Event) {
	const sendTimeout = time.Minute

	t := time.NewTimer(sendTimeout)
	defer close(outEvents)
	defer t.Stop()

	sendEvent := func(e *upspin.Event) bool {
		// Send e on outEvents, with a timeout.
		if !t.Stop() {
			<-t.C
		}
		t.Reset(sendTimeout)
		select {
		case outEvents <- *e:
			// OK, sent.
			return true
		case <-t.C:
			// Timed out.
			log.Printf("%s: timeout sending event for %s", op, s.userName)
			return false
		}
	}

	for {
		e, ok := <-treeEvents
		if !ok {
			// Tree closed channel. Close outgoing event as well.
			return
		}
		if e.Entry == nil {
			// It's likely an error. Pass it along. We're sure to
			// have treeEvents closed in the next loop.
			sendEvent(e)
			continue
		}

		// Check permissions on e.Entry.
		p, err := path.Parse(e.Entry.Name)
		if err != nil {
			sendEvent(&upspin.Event{Error: errors.E(op, err)})
			return
		}
		hasAny, _, err := s.hasRight(access.AnyRight, p)
		if err != nil {
			sendEvent(&upspin.Event{Error: errors.E(op, err)})
			return
		}
		if !hasAny {
			continue
		}
		hasRead, _, err := s.hasRight(access.Read, p)
		if err != nil {
			sendEvent(&upspin.Event{Error: errors.E(op, err)})
			return
		}
		if !hasRead {
			if !access.IsAccessControlFile(e.Entry.SignedName) {
				e.Entry.MarkIncomplete()
			}
		}
		if !sendEvent(e) {
			return
		}
	}
}

// Dial implements upspin.Dialer.
func (s *server) Dial(ctx upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "dir/server.Dial"
	if e.Transport == upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "transport must not be unassigned")
	}
	if err := valid.UserName(ctx.UserName()); err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	cp := *s // copy of the generator instance.
	// Overwrite the userName and its sub-components (base, suffix, domain).
	cp.userName = ctx.UserName()
	cp.dialed = true
	var err error
	cp.userBase, cp.userSuffix, cp.userDomain, err = user.Parse(cp.userName)
	if err != nil {
		return nil, errors.E(op, err)
	}

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
	return s.serverConfig.DirEndpoint()
}

// Close implements upspin.Service.
func (s *server) Close() {
	const op errors.Op = "dir/server.Close"

	// Remove this user's tree from the cache. This allows it to be
	// garbage-collected even if other servers have pointers into the
	// cache (which at least one will have, the one created with New).
	if err := s.closeTree(s.userName); err != nil {
		log.Error.Printf("%s: Error closing user tree %q: %q", op, s.userName, err)
	}

	if !s.dialed {
		s.shutdown()
	}
}

func (s *server) closeTree(user upspin.UserName) error {
	defer s.userLock(s.userName).Unlock()

	if t, ok := s.userTrees.Remove(user).(*tree.Tree); ok {
		// Close will flush and release all resources.
		if err := t.Close(); err != nil {
			return err
		}
	}
	return nil
}

// loadTreeFor loads the user's tree, if it exists.
func (s *server) loadTreeFor(userName upspin.UserName, opts ...options) (*tree.Tree, error) {
	defer span(opts).StartSpan("loadTreeFor").End()

	if err := valid.UserName(userName); err != nil {
		return nil, errors.E(errors.Invalid, err)
	}

	defer s.userLock(s.userName).Unlock()

	// Do we have a cached tree for this user already?
	if val, found := s.userTrees.Get(userName); found {
		if tree, ok := val.(*tree.Tree); ok {
			return tree, nil
		}
		// This should never happen because we only store type tree.Tree in the userTree.
		return nil, errors.E(userName, errors.Internal,
			errors.Errorf("userTrees contained value of unexpected type %T", val))
	}
	// User is not in the cache. Load a tree from the logs, if they exist.
	hasLog, err := serverlog.HasLog(userName, s.logDir)
	if err != nil {
		return nil, err
	}
	if !hasLog && !s.canCreateRoot(userName) {
		// Tree for user does not exist and the logged-in user is not
		// allowed to create it.
		return nil, errNotExist
	}
	user, err := serverlog.Open(userName, s.logDir, s.serverConfig.Factotum(), s.storage)
	if err != nil {
		return nil, err
	}
	// If user has root, we can load the tree from it.
	if _, err := user.Root(); err != nil {
		// Likely the user has no root yet.
		if !errors.Is(errors.NotExist, err) {
			// No it's some other error. Abort.
			return nil, err
		}
		// Ok, let it proceed. The  user will still need to make the
		// root, but we allow setting up a new tree for now.
		err = user.SaveOffset(0)
		if err != nil {
			return nil, err
		}
		// Fall through and load a new tree.
	}
	// Create a new tree for the user.
	tree, err := tree.New(s.serverConfig, user)
	if err != nil {
		return nil, err
	}
	// Add to the cache and return
	s.userTrees.Add(userName, tree)
	return tree, nil
}

// canCreateRoot reports whether the current user can create a root for the
// named user.
func (s *server) canCreateRoot(user upspin.UserName) bool {
	if s.userName == user {
		return true
	}
	if isSnapshotUser(user) && s.isSnapshotOwner(user) {
		return true
	}
	return false
}

// errPerm checks whether the user has any right to the given path, and if so
// returns a Permission error. Otherwise it returns a Private error.
// This is used to prevent probing of the name space.
func (s *server) errPerm(op errors.Op, p path.Parsed, opts ...options) error {
	// Before returning, check that the user has the right to know,
	// to prevent leaking the name space.
	if hasAny, _, err := s.hasRight(access.AnyRight, p, opts...); err != nil {
		// Some error other than ErrFollowLink.
		return errors.E(op, err)
	} else if !hasAny {
		// User does not have Any right. Return a 'Private' error.
		return errors.E(op, p.Path(), errors.Private)
	}
	return errors.E(op, p.Path(), access.ErrPermissionDenied)
}

// errLink checks whether the user has any right to the given entry, and if so
// returns the entry and ErrFollowLink. If the use has no rights, it returns a
// NotExist error. This is used to prevent probing of the name space using
// links.
func (s *server) errLink(op errors.Op, link *upspin.DirEntry, opts ...options) (*upspin.DirEntry, error) {
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
	// Denied. User has no right on link. Return a 'Private' error.
	return nil, errors.E(op, p.Path(), errors.Private)
}

// shutdown is called when the server is being forcefully shut down.
func (s *server) shutdown() {
	it := s.userTrees.NewIterator()
	for {
		k, _, next := it.GetAndAdvance()
		if !next {
			break
		}
		user := k.(upspin.UserName)
		err := s.closeTree(user)
		if err != nil {
			log.Printf("error closing tree for user %s: %v", user, err)
		}
	}
}

// newOptMetric creates a new options populated with a metric for operation op.
func newOptMetric(op errors.Op) (options, *metric.Metric) {
	m, sp := metric.NewSpan(op)
	opts := options{
		span: sp,
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
func subspan(op errors.Op, opts []options) (options, *metric.Span) {
	s := span(opts).StartSpan(op)
	return options{span: s}, s
}
