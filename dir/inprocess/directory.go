// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple, non-persistent, in-memory directory service.
package inprocess // import "upspin.io/dir/inprocess"

// The implementation uses a Merkle tree to represent the directory tree.
// For simplicity, a directory's data is always stored as a single block.
// (The files it stores may have any number of blocks.)
// Even empty directories contain a single zero-sized block.
// For the purposes of the Merkle tree, the reference is stored in entry.Blocks[0].Location.

import (
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/serverutil"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"

	_ "upspin.io/pack/ee"
)

func New(config upspin.Config) upspin.DirServer {
	return &server{
		config: config,
		db: &database{
			dirConfig:  config,
			root:       make(map[upspin.UserName]*upspin.DirEntry),
			seq:        make(map[upspin.UserName]int64),
			rootAccess: make(map[upspin.UserName]*access.Access),
			access:     make(map[upspin.PathName]*access.Access),
			eventMgr:   newEventManager(),
		},
	}
}

// Used to store directory entries.
// All directories are encoded with this packing.
var dirPacking = upspin.EEPack

// server implements the upspin.DirServer interface. It is a multiplexed
// by user onto a database.
type server struct {
	// config holds the config that created the call.
	config upspin.Config
	db     *database
}

var _ upspin.DirServer = (*server)(nil)

// database represents the shared state of the directory forest.
type database struct {
	dirConfig upspin.Config // For accessing store holding directory entries.
	eventMgr  *eventManager // Handles Watch events.

	// mu is used to serialize access to the maps.
	// It's also used to serialize all access to the store through the
	// exported API, for simple but slow safety. At least it's an RWMutex
	// so it's not _too_ bad.
	mu sync.RWMutex

	// root stores the directory entry for each user's root.
	root map[upspin.UserName]*upspin.DirEntry

	// seq stores the most recent sequence number for each user's root.
	seq map[upspin.UserName]int64

	// rootAccess stores the default Access file for each user's root.
	// Computed lazily and only used if needed.
	rootAccess map[upspin.UserName]*access.Access

	// access stores the parsed contents of any Access file stored
	// in this directory. Inherited rights are computed from this map.
	access map[upspin.PathName]*access.Access
}

// startSequence starts the next sequence number for this user. db must be locked.
func (db *database) startSequence(user upspin.UserName) {
	db.seq[user] = upspin.SeqBase
}

// incSequence advances the next sequence number for this user. db must be locked.
func (db *database) incSequence(user upspin.UserName) {
	s := db.seq[user]
	s++
	db.seq[user] = s
}

// sequence returns the current sequence number for this user. db must be locked.
func (db *database) sequence(user upspin.UserName) int64 {
	return db.seq[user]
}

var _ upspin.DirServer = (*server)(nil)

// newDirEntry returns a new DirEntry holding the provided directory data (cleartext).
// This is the general form of the method that follows, used in the tests.
func newDirEntry(config upspin.Config, packing upspin.Packing, name upspin.PathName, cleartext []byte, attr upspin.Attribute, link upspin.PathName, seq int64) (*upspin.DirEntry, error) {
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name, // TODO: snapshots.
		Packing:    packing,
		Time:       upspin.Now(),
		Attr:       attr,
		Link:       link,
		Sequence:   seq,
		Writer:     config.UserName(),
	}
	if (link != "") != (attr == upspin.AttrLink) {
		return nil, errors.Errorf("inconsistent attribute (%v) and link (%q) fields", attr, link)
	}
	packer := pack.Lookup(packing)
	if packer == nil {
		return nil, errors.Errorf("no packing %#x registered", packing)
	}
	if attr == upspin.AttrLink {
		// No data to pack.
		return entry, nil
	}
	bp, err := packer.Pack(config, entry)
	if err != nil {
		return nil, err
	}
	ciphertext, err := bp.Pack(cleartext)
	if err != nil {
		return nil, err
	}
	store, err := bind.StoreServer(config, config.StoreEndpoint())
	if err != nil {
		return nil, err
	}
	refdata, err := store.Put(ciphertext)
	if err != nil {
		return nil, err
	}
	bp.SetLocation(
		upspin.Location{
			Endpoint:  config.StoreEndpoint(),
			Reference: refdata.Reference,
		},
	)
	if err := bp.Close(); err != nil {
		return nil, err
	}
	return entry, nil
}

// newDirEntry returns a new DirEntry holding the provided directory data (cleartext).
// It is called for directories only.
func (s *server) newDirEntry(name upspin.PathName, cleartext []byte, seq int64) (*upspin.DirEntry, error) {
	return newDirEntry(s.db.dirConfig, dirPacking, name, cleartext, upspin.AttrDirectory, "", seq)
}

// makeRoot creates a new user root.
// s.db is locked.
func (s *server) makeRoot(parsed path.Parsed) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.makeRoot"
	// Creating a root: easy!
	// Only the owner can create the root, but the canPut check is sufficient since a
	// non-existent root has no Access file yet.
	if _, present := s.db.root[parsed.User()]; present {
		return nil, errors.E(op, parsed.Path(), errors.Exist)
	}
	// We will have a zero-sized block here, which is odd but necessary to have
	// a place to store the directory's Reference.
	s.db.startSequence(parsed.User())
	entry, err := s.newDirEntry(upspin.PathName(parsed.User()+"/"), nil, s.db.sequence(parsed.User()))
	if err != nil {
		return nil, err
	}
	s.db.root[parsed.User()] = entry
	return entry, nil
}

// Put implements upspin.DirServer.Put.
func (s *server) Put(argEntry *upspin.DirEntry) (*upspin.DirEntry, error) {
	// Copy the argument because we don't want to overwrite fields such as Sequence in caller.
	entry := new(upspin.DirEntry)
	*entry = *argEntry
	const op errors.Op = "dir/inprocess.Put"
	if err := valid.DirEntry(entry); err != nil {
		return nil, errors.E(op, err)
	}
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err) // Can't happen but be sure.
	}
	e, err := s.canPut(op, parsed, entry.IsDir())
	if err != nil {
		return s.errLink(op, e, err)
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	isAccess := access.IsAccessFile(entry.Name)
	isGroup := access.IsGroupFile(entry.Name)
	if isAccess || isGroup {
		if isAccess && entry.IsDir() {
			// A Group file may be in a subdirectory; it's only Access files we worry about.
			return nil, errors.E(op, entry.Name, errors.Invalid, "cannot create a directory named Access")
		}
		if !entry.IsDir() {
			packer := pack.Lookup(entry.Packing)
			if packer == nil {
				return nil, errors.E(op, entry.Name, errors.Errorf("unknown packing %d", entry.Packing))
			}
			ok, err := packer.UnpackableByAll(entry)
			if err != nil {
				return nil, errors.E(op, entry.Name, err)
			}
			if !ok {
				return nil, errors.E(op, entry.Name, "Access or Group files must be readable by access.AllUsers")
			}
		}
		if entry.IsLink() {
			return nil, errors.E(op, entry.Name, "cannot create a link named Access or Group")
		}
		if isGroup {
			// Check that the name is a legal Group name.
			// All elements must satisfy this condition, to protect Access file parsing.
			// TODO: Is this the syntax we should require for any Upspin name?
			for i := 1; i < parsed.NElem(); i++ { // Element 0 is "Group".
				if _, _, err := user.ParseUser(parsed.Elem(i)); err != nil {
					return nil, errors.E(op, entry.Name, err)
				}
			}
		}
	}

	if entry.IsDir() && parsed.IsRoot() {
		// Making a root.
		entry, err = s.makeRoot(parsed)
	} else if !entry.IsDir() {
		// Making a new regular entry.
		entry, err = s.put(op, entry, parsed, false)
	} else {
		// Making a new directory.
		entry, err = s.newDirEntry(entry.Name, []byte(""), entry.Sequence)
		if err != nil {
			return nil, err
		}
		entry, err = s.put(op, entry, parsed, false)
	}
	if err != nil {
		return nil, err
	}
	s.db.eventMgr.newEvent <- upspin.Event{
		Entry: entry,
	}
	// Successful Put returns incomplete DirEntry holding only the sequence number.
	retEntry := &upspin.DirEntry{
		Attr:     upspin.AttrIncomplete,
		Sequence: entry.Sequence,
	}
	return retEntry, nil
}

// canPut verifies that the name is permitted to be written.
// It may return ErrFollowLink.
// s.db.mu must not be held, which means races are possible
// but they are not harmful (are they?).
func (s *server) canPut(op errors.Op, parsed path.Parsed, makeDirectory bool) (*upspin.DirEntry, error) {
	name := parsed.Path()
	if makeDirectory && parsed.IsRoot() {
		// We're fine.
	} else {
		// The file can't be the root.
		// The parent directory must exist (and be a directory).
		if parsed.IsRoot() {
			return nil, errors.E(op, parsed.Path(), errors.IsDir)
		}
		parent, err := s.lookup(op, parsed.Drop(1), true)
		if err != nil {
			return parent, err // Probably ErrFollowLink or NotExist.
		}
		if !parent.IsDir() {
			return nil, errors.E(op, parent.Name, errors.NotDir)
		}
	}
	// The child might exist.
	existing, err := s.lookup(op, parsed, true)
	if err == upspin.ErrFollowLink {
		return existing, err
	}
	if existing != nil && existing.IsDir() {
		// TODO: figure out whether this should be Exist or NotDir
		return nil, errors.E(op, name, errors.Exist) // Cannot overwrite directory.
	}
	// We know the full path has no links.
	if existing == nil {
		// New file, need create permission.
		canCreate, err := s.can(access.Create, parsed)
		if err != nil {
			return nil, errors.E(op, name, err)
		}
		if !canCreate {
			return nil, s.errPerm(op, parsed)
		}
		return nil, nil
	}

	canWrite, err := s.can(access.Write, parsed)
	if err != nil {
		return nil, errors.E(op, name, err)
	}
	if !canWrite {
		return nil, s.errPerm(op, parsed)
	}
	return nil, nil
}

// put is the underlying implementation of Put, including making links and directories..
// If deleting, we expect the entry to already be present and skip it on the rewrite.
func (s *server) put(op errors.Op, entry *upspin.DirEntry, parsed path.Parsed, deleting bool) (*upspin.DirEntry, error) {
	pathName := parsed.Path()
	if parsed.IsRoot() {
		// Should not be here.
		return nil, errors.E(op, pathName, errors.Internal, "cannot create root with s.put")
	}
	rootEntry, ok := s.db.root[parsed.User()]
	if !ok {
		// Cannot create user root with Put.
		return nil, errors.E(op, upspin.PathName(parsed.User()), "no such user root")
	}
	// Iterate along the path up to but not past the last element.
	// We remember the entries as we descend for fast(er) overwrite of the Merkle tree.
	// Invariant: dirRef refers to a directory.
	entries := make([]*upspin.DirEntry, 0, 10) // 0th entry is the root.
	entries = append(entries, rootEntry)
	for i := 0; i < parsed.NElem()-1; i++ {
		e, err := s.fetchEntry(op, rootEntry, parsed.Elem(i))
		if err != nil {
			return nil, err
		}
		if e.IsLink() {
			return e, upspin.ErrFollowLink
		}
		if !e.IsDir() {
			return nil, errors.E(op, parsed.First(i+1).Path(), errors.NotDir)
		}
		entries = append(entries, e)
		rootEntry = e
	}
	// We're adding an item (probably). Advance the sequence number. If the put fails for some reason,
	// it's OK - the sequence number will be just be larger next time.
	s.db.incSequence(parsed.User())
	rootEntry, dirBlob, err := s.installEntry(op, path.DropPath(pathName, 1), rootEntry, entry, deleting, false)
	if err != nil {
		return nil, err
	}
	// Rewrite the tree up to the root.
	// Invariant: dirRef identifies the directory that has just been updated,
	// and its payload is in dirBlob.
	// i indicates the directory that needs to be updated to store the new dirRef.
	for i := len(entries) - 2; i >= 0; i-- {
		// Install into the ith directory the (i+1)th entry.
		rootEntry, err = s.newDirEntry(entries[i+1].Name, dirBlob, entries[i+1].Sequence)
		if err != nil {
			return nil, err
		}
		rootEntry, dirBlob, err = s.installEntry(op, parsed.First(i).Path(), entries[i], rootEntry, false, true)
		if err != nil {
			// TODO: System is now inconsistent.
			return nil, err
		}
	}
	// Update the root.
	s.db.root[parsed.User()] = rootEntry
	if access.IsGroupFile(entry.Name) {
		if entry.IsLink() {
			return nil, errors.E(op, errors.Internal, entry.Name, "Group file cannot be a link")
		}
		// Group files are loaded on demand but we must wipe the cache.
		access.RemoveGroup(entry.Name)
	} else if access.IsAccessFile(entry.Name) {
		if entry.IsLink() {
			return nil, errors.E(op, errors.Internal, entry.Name, "Access file cannot be a link")
		}
		var accessFile *access.Access
		if !deleting {
			data, err := s.readAll(entry)
			if err != nil {
				return nil, errors.E(op, err)
			}
			accessFile, err = access.Parse(entry.Name, data)
			if err != nil {
				return nil, errors.E(op, err)
			}
		}
		s.db.access[path.DropPath(entry.Name, 1)] = accessFile
	}

	return entry, nil
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (s *server) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.WhichAccess"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// Does the item exist?
	entry, err := s.lookup(op, parsed, true)
	if err == upspin.ErrFollowLink {
		return s.errLink(op, entry, err)
	}
	if errors.Is(errors.NotExist, err) {
		// The parent must exist.
		_, err = s.lookup(op, parsed.Drop(1), true)
		if err != nil {
			// Always say Private to avoid giving information away.
			// We know it's not a link.
			return nil, errors.E(op, pathName, errors.Private)
		}
	}
	// Now we know the path is valid in our space, with no links.
	// If the user has any right for this file, we can show the relevant Access file.
	canKnow, err := s.can(access.AnyRight, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canKnow {
		// Don't tell the user this path exists.
		return nil, errors.E(op, pathName, errors.Private)
	}
	accessFile := s.whichAccess(parsed)
	if accessFile == nil {
		return nil, nil
	}
	parsed, err = path.Parse(accessFile.Path())
	if err != nil {
		// Can't happen.
		return nil, errors.E(op, errors.Internal, err)
	}
	return s.lookup(op, parsed, true)
}

// whichAccess is the workings of WhichAccess, accepting a parsed path name
// and returning a parsed access file. We know that the path contains no links
// along it, including at the last element. Therefore we can work from the
// innermost element downwards.
func (s *server) whichAccess(parsed path.Parsed) *access.Access {
	for {
		s.db.mu.RLock()
		accessFile := s.db.access[parsed.Path()]
		s.db.mu.RUnlock()
		if accessFile != nil {
			return accessFile
		}
		if parsed.IsRoot() {
			// We've reached the root but there is no access file there.
			return nil
		}
		// Step up to parent directory. // TODO: This is incorrect in the presence of links.
		parsed = parsed.Drop(1)
	}
}

// Watch implements upspin.DirServer.Watch.
func (s *server) Watch(name upspin.PathName, seq int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	const op errors.Op = "dir/inprocess.Watch"
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// The root must exist.
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	if s.db.root[parsed.User()] == nil {
		return nil, errors.E(op, name, errors.NotExist)
	}
	return s.db.eventMgr.watch(s, parsed, seq, done)
}

// readAll retrieves the data for the entry.
func (s *server) readAll(entry *upspin.DirEntry) ([]byte, error) {
	return clientutil.ReadAll(s.db.dirConfig, entry)
}

// Delete implements upspin.DirServer.Delete.
func (s *server) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.Delete"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := s.lookup(op, parsed, false) // File must exist, but may have intermediate link.
	if err != nil {
		return s.errLink(op, entry, err)
	}

	// There are no links.
	canDelete, err := s.can(access.Delete, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canDelete {
		return nil, s.errPerm(op, parsed)
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	// If it is a directory, it must be empty.
	if entry.IsDir() {
		if !s.isEmptyDirectory(op, entry) {
			return nil, errors.E(op, pathName, errors.NotEmpty)
		}
		if parsed.IsRoot() {
			delete(s.db.root, parsed.User())
			return nil, nil // Nothing else to do.
		}
	}

	entry, err = s.put(op, entry, parsed, true)
	if err == nil {
		s.db.eventMgr.newEvent <- upspin.Event{
			Entry:  entry,
			Delete: true,
		}
	}
	return entry, err
}

func (s *server) isEmptyDirectory(op errors.Op, entry *upspin.DirEntry) bool {
	if !entry.IsDir() {
		return false
	}
	size, err := entry.Size()
	if err != nil {
		panic(err)
	}
	return size == 0
}

// Lookup implements upspin.DirServer.Lookup.
func (s *server) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.Lookup"
	log.Debug.Println("Lookup", pathName)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := s.lookup(op, parsed, true)
	if err != nil {
		if errors.Is(errors.NotExist, err) {
			if canAny, err := s.can(access.AnyRight, parsed); err != nil {
				return nil, err
			} else if !canAny {
				return nil, errors.E(op, pathName, errors.Private)
			}
		}
		return s.errLink(op, entry, err)
	}
	// There were no links.
	canRead, err := s.can(access.Read, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		canAny, err := s.can(access.AnyRight, parsed)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if !canAny {
			return nil, s.errPerm(op, parsed)
		}
		if !access.IsAccessControlFile(entry.SignedName) {
			entry.MarkIncomplete()
		}
	}
	return entry, nil
}

// lookup is the internal version of lookup; it does not do any Access checks.
func (s *server) lookup(op errors.Op, parsed path.Parsed, followFinal bool) (*upspin.DirEntry, error) {
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	dirEntry, ok := s.db.root[parsed.User()]
	if !ok {
		return nil, errors.E(upspin.PathName(parsed.User()), errors.NotExist, errors.Str("no such user"))
	}
	if parsed.IsRoot() {
		return dirEntry, nil
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	for i := 0; i < parsed.NElem()-1; i++ {
		entry, err := s.fetchEntry(op, dirEntry, parsed.Elem(i))
		if err != nil {
			return nil, err
		}
		if entry.IsLink() {
			return entry, upspin.ErrFollowLink
		}
		if !entry.IsDir() {
			return nil, errors.E(op, parsed.Path(), errors.NotExist)
		}
		dirEntry = entry
	}
	lastElem := parsed.Elem(parsed.NElem() - 1)
	// Destination must exist. If so we need to update the parent directory record.
	entry, err := s.fetchEntry(op, dirEntry, lastElem)
	if err != nil {
		return nil, err
	}
	if entry.IsLink() && followFinal {
		return entry, upspin.ErrFollowLink
	}
	return entry, nil
}

// Glob implements upspin.DirServer.Glob.
func (s *server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.Glob"
	log.Debug.Print(pattern)

	entries, err := serverutil.Glob(pattern, s.Lookup, s.listDir)
	if err != nil && err != upspin.ErrFollowLink {
		err = errors.E(op, err)
	}
	return entries, err
}

// listDir implements serverutil.ListFunc.
// dirName should always be a directory.
func (s *server) listDir(dirName upspin.PathName) ([]*upspin.DirEntry, error) {
	const op errors.Op = "dir/inprocess.Glob" // The only (indirect) caller of this function.
	log.Debug.Println("listDir", dirName)

	parsed, err := path.Parse(dirName)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Fetch the directory's DirEntry.
	dir, listErr := s.lookup(op, parsed, true)
	if listErr == upspin.ErrFollowLink {
		return []*upspin.DirEntry{dir}, listErr
	}

	// Check that we have list rights for the directory.
	canList, err := s.can(access.List, parsed)
	if err != nil {
		// TODO(adg): this error needs sanitizing
		return nil, errors.E(op, dirName, err)
	}
	if !canList {
		return nil, errors.E(op, dirName, errors.Private)
	}
	if listErr != nil {
		return nil, listErr
	}
	if !dir.IsDir() {
		return nil, errors.E(op, dir.Name, errors.NotDir)
	}

	// Fetch the directory's contents.
	payload, err := s.readAll(dir)
	if err != nil {
		return nil, errors.E(op, dir.Name, errors.Internal, errors.Str("invalid reference: "+err.Error()))
	}
	canRead, _ := s.can(access.Read, parsed)
	var results []*upspin.DirEntry
	for len(payload) > 0 {
		var e upspin.DirEntry
		payload, err = e.Unmarshal(payload)
		if err != nil {
			return nil, errors.E(op, dir.Name, err)
		}
		if !canRead {
			if !access.IsAccessControlFile(e.SignedName) {
				e.MarkIncomplete()
			}
		}
		results = append(results, &e)
	}
	return results, nil
}

// can reports whether the calling user (defined by s.config.UserName()) has the
// access right for this file or directory.
// s.db.mu is _not_ held.
func (s *server) can(right access.Right, parsed path.Parsed) (bool, error) {
	accessFile := s.whichAccess(parsed)
	if accessFile == nil {
		accessFile = s.rootAccessFile(parsed)
	}
	return accessFile.Can(s.config.UserName(), right, parsed.Path(), s.load)
}

// errPerm checks whether the user has any right to the
// given path, and if so returns a Permission error.
// Otherwise it returns a Private error.
// This is used to prevent probing of the name space.
func (s *server) errPerm(op errors.Op, parsed path.Parsed) error {
	canKnow, err := s.can(access.AnyRight, parsed)
	if err != nil {
		return errors.E(op, parsed.Path(), err)
	}
	if !canKnow {
		return errors.E(op, parsed.Path(), errors.Private)
	}
	return errors.E(op, parsed.Path(), errors.Permission)
}

// errLink is intended to prevent probing of the name space
// using links. If errArg is not ErrFollowLink, the arguments
// are just returned. Otherwise, it checks whether the user
// has any right to the given entry, and if so returns the entry
// and ErrFollowLink. If the use has no rights, it returns a
// Private error.
func (s *server) errLink(op errors.Op, entry *upspin.DirEntry, errArg error) (*upspin.DirEntry, error) {
	if errArg != upspin.ErrFollowLink {
		return entry, errArg
	}
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, errors.Internal, entry.Name, err)
	}
	canKnow, err := s.can(access.AnyRight, parsed)
	if err != nil {
		return nil, errors.E(op, errors.Internal, parsed.Path(), err)
	}
	if !canKnow {
		return nil, errors.E(op, parsed.Path(), errors.Private)
	}
	return entry, errArg
}

// load is a helper for Access.Can that gets the entire contents of the named item.
func (s *server) load(name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	entry, err := s.lookup("access", parsed, true)
	if err != nil {
		return nil, err
	}
	return s.readAll(entry)
}

// rootAccess file returns the parsed Access file providing default permissions for the root of this path.
func (s *server) rootAccessFile(parsed path.Parsed) *access.Access {
	s.db.mu.RLock()
	accessFile := s.db.rootAccess[parsed.User()]
	s.db.mu.RUnlock()
	if accessFile == nil {
		var err error
		accessFile, err = access.New(parsed.Path())
		if err != nil {
			panic(err)
		}
		s.db.mu.Lock()
		s.db.rootAccess[parsed.User()] = accessFile
		s.db.mu.Unlock()
	}
	return accessFile
}

// fetchEntry returns the reference for the named elem within the directory referenced by dirEntry.
// It reads the whole directory, so avoid calling it repeatedly.
func (s *server) fetchEntry(op errors.Op, entry *upspin.DirEntry, elem string) (*upspin.DirEntry, error) {
	payload, err := s.readAll(entry)
	if err != nil {
		return nil, err
	}
	return s.dirEntLookup(op, entry.Name, payload, elem)
}

// dirEntLookup returns the ref for the entry in the named directory whose contents are given in the payload.
// The boolean is true if the entry itself describes a directory.
func (s *server) dirEntLookup(op errors.Op, pathName upspin.PathName, payload []byte, elem string) (*upspin.DirEntry, error) {
	if len(elem) == 0 {
		return nil, errors.E(op, pathName, "empty path name element")
	}
	fileName := path.Join(pathName, elem)
	var entry upspin.DirEntry
Loop:
	for len(payload) > 0 {
		remaining, err := entry.Unmarshal(payload)
		if err != nil {
			return nil, errors.E(op, err)
		}
		payload = remaining
		if fileName != entry.Name {
			continue Loop
		}
		return &entry, nil
	}
	return nil, errors.E(op, fileName, errors.NotExist)
}

var errSeq = errors.Str("sequence mismatch")

// installEntry installs the new entry in the directory referenced by the dirEntry, appending or overwriting the
// entry as required. It returns the entry of the updated directory and the blob itself.
func (s *server) installEntry(op errors.Op, dirName upspin.PathName, dirEntry *upspin.DirEntry, newEntry *upspin.DirEntry, deleting, dirOverwriteOK bool) (*upspin.DirEntry, []byte, error) {
	dirData, err := s.readAll(dirEntry)
	if err != nil {
		return nil, nil, err
	}
	found := false
	var nextEntry upspin.DirEntry
	for payload := dirData; len(payload) > 0; {
		// Remember where this entry starts.
		start := len(dirData) - len(payload)
		remaining, err := nextEntry.Unmarshal(payload)
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		length := len(payload) - len(remaining)
		payload = remaining
		if nextEntry.Name != newEntry.Name {
			continue
		}
		// We found the item with that name.
		// If it is a link, we error out unless we are deleting it.
		if nextEntry.IsLink() && !deleting {
			return &nextEntry, nil, upspin.ErrFollowLink
		}
		found = true
		if !deleting {
			// If it's already there and the sequence number is SeqNotExist, this is an error.
			if newEntry.Sequence == upspin.SeqNotExist {
				return nil, nil, errors.E(op, newEntry.Name, errors.Exist)
			}
			// If it's already there and is not expected to be a directory, this is an error.
			if nextEntry.IsDir() && !dirOverwriteOK {
				return nil, nil, errors.E(op, errors.IsDir, dirName, "cannot overwrite directory")
			}
		}
		// Drop this entry so we can append the updated one (or skip it, if we're deleting).
		// It may have changed length because of the metadata being unpredictable,
		// so we cannot overwrite it in place.
		copy(dirData[start:], remaining)
		dirData = dirData[:len(dirData)-length]
		if !deleting {
			// We want nextEntry's sequence but everything else from newEntry.
			if newEntry.Sequence != upspin.SeqIgnore {
				if newEntry.Sequence != nextEntry.Sequence {
					return nil, nil, errors.E(op, newEntry.Name, errSeq)
				}
			}
			newEntry.Sequence = nextEntry.Sequence
		}
		break
	}
	parsed, err := path.Parse(newEntry.Name)
	if err != nil {
		// Cannot happen but be safe.
		return nil, nil, errors.E(op, err)
	}
	seq := s.db.sequence(parsed.User())
	if deleting {
		// Must exist.
		if !found {
			return nil, nil, errors.E(op, newEntry.Name, errors.NotExist)
		}
		newEntry.Sequence = seq
	} else {
		if !found {
			// The provided sequence number may be only SeqNotExist or SeqIgnore.
			if newEntry.Sequence != upspin.SeqNotExist && newEntry.Sequence != upspin.SeqIgnore {
				return nil, nil, errors.E(op, parsed.Path(), errors.Invalid, "invalid sequence number")
			}
		}
		// Add new entry to directory.
		newEntry.Sequence = seq
		data, err := newEntry.Marshal()
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		dirData = append(dirData, data...)
	}
	entry, err := s.newDirEntry(dirName, dirData, seq)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}
	return entry, dirData, nil
}

// Methods to implement upspin.Dialer.

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "dir/inprocess.Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}
	this := *s // Make a copy.
	this.config = config
	return &this, nil
}

// Endpoint implements upspin.DirServer.Endpoint.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // Ignored.
	}
}

// Close implements upspin.server.
func (s *server) Close() {
	// TODO: unimplemented.
}
