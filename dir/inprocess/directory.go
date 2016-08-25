// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple, non-persistent, in-memory directory service.
package inprocess

// The implementation uses a Merkle tree to represent the directory tree.
// For simplicity, a directory's data is always stored as a single block.
// (The files it stores may have any number of blocks.)
// Even empty directories contain a single zero-sized block.
// For the purposes of the Merkle tree, the reference is stored in entry.Blocks[0].Location.

// TODO: If links are present, access control will use wrong Access file. Needs to be fixed!

import (
	goPath "path"

	"strings"
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/valid"

	_ "upspin.io/pack/debug" // Used to pack directory entries.
	_ "upspin.io/pack/plain"
)

func New(context upspin.Context) upspin.DirServer {
	return &server{
		context: context,
		db: &database{
			dirContext: context,
			root:       make(map[upspin.UserName]*upspin.DirEntry),
			rootAccess: make(map[upspin.UserName]*access.Access),
			access:     make(map[upspin.PathName]*access.Access),
		},
	}
}

// Used to store directory entries.
// All directories are encoded with this packing.
var (
	dirPacking = upspin.DebugPack
	dirPacker  = pack.Lookup(dirPacking)
)

// server implements the upspin.DirServer interface. It is a multiplexed
// by user onto a database.
type server struct {
	upspin.NoConfiguration
	// context holds the context that created the call.
	context upspin.Context
	db      *database
}

var _ upspin.DirServer = (*server)(nil)

const transport = upspin.InProcess

func init() {
	bind.RegisterDirServer(transport, New(nil))
}

// database represents the shared state of the directory forest.
type database struct {
	dirContext upspin.Context // For accessing store holding directory entries.

	// mu is used to serialize access to the maps.
	// It's also used to serialize all access to the store through the
	// exported API, for simple but slow safety. At least it's an RWMutex
	// so it's not _too_ bad.
	mu sync.RWMutex

	// dialed reports whether the service has its first Dialed connection.
	dialed bool

	// root stores the directory entry for each user's root.
	root map[upspin.UserName]*upspin.DirEntry

	// rootAccess stores the default Access file for each user's root.
	// Computed lazily and only used if needed.
	rootAccess map[upspin.UserName]*access.Access

	// access stores the parsed contents of any Access file stored
	// in this directory. Inherited rights are computed from this map.
	access map[upspin.PathName]*access.Access
}

var _ upspin.DirServer = (*server)(nil)

// newDirEntry returns a new DirEntry holding the provided directory data (cleartext).
// This is the general form of the method that follows, used in the tests.
func newDirEntry(context upspin.Context, packing upspin.Packing, name upspin.PathName, cleartext []byte, attr upspin.Attribute, link upspin.PathName, seq int64) (*upspin.DirEntry, error) {
	entry := &upspin.DirEntry{
		Name:     name,
		Packing:  packing,
		Time:     upspin.Now(),
		Attr:     attr,
		Link:     link,
		Sequence: seq,
		Writer:   context.UserName(),
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
	bp, err := packer.Pack(context, entry)
	if err != nil {
		return nil, err
	}
	ciphertext, err := bp.Pack(cleartext)
	if err != nil {
		return nil, err
	}
	store, err := bind.StoreServer(context, context.StoreEndpoint())
	if err != nil {
		return nil, err
	}
	ref, err := store.Put(ciphertext)
	if err != nil {
		return nil, err
	}
	bp.SetLocation(
		upspin.Location{
			Endpoint:  context.StoreEndpoint(),
			Reference: ref,
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
	return newDirEntry(s.db.dirContext, dirPacking, name, cleartext, upspin.AttrDirectory, "", seq)
}

// dirBlock constructs an upspin.DirBlock with the appropriate fields.
func dirBlock(context upspin.Context, ref upspin.Reference, offset int64, blob []byte) upspin.DirBlock {
	return upspin.DirBlock{
		Location: upspin.Location{
			Endpoint:  context.StoreEndpoint(),
			Reference: ref,
		},
		Offset: offset,
		Size:   int64(len(blob)),
	}
}

// MakeDirectory implements upspin.DirServer.MakeDirectory.
func (s *server) MakeDirectory(directoryName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/inprocess.MakeDirectory"
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	parsed, err := path.Parse(directoryName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	canCreate, err := s.can(access.Create, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canCreate {
		return nil, errors.E(op, directoryName, access.ErrPermissionDenied)
	}
	pathName := parsed.Path()
	if access.IsAccessFile(pathName) || access.IsGroupFile(pathName) {
		return nil, errors.E(op, directoryName, errors.Str("cannot create directory named Access or Group"))
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if parsed.IsRoot() {
		// Creating a root: easy!
		// Only the onwer can create the root, but the check above is sufficient since a
		// non-existent root has no Access file yet.
		if _, present := s.db.root[parsed.User()]; present {
			return nil, errors.E(op, directoryName, errors.Exist)
		}
		// We will have a zero-sized block here, which is odd but necessary to have
		// a place to store the directory's Reference.
		entry, err := s.newDirEntry(upspin.PathName(parsed.User()+"/"), nil, 0)
		if err != nil {
			return nil, err
		}
		s.db.root[parsed.User()] = entry
		return entry, nil
	}
	entry, err := s.newDirEntry(parsed.Path(), nil, 0)
	if err != nil {
		return nil, err
	}
	return s.put(op, entry, false)
}

// Put implements upspin.DirServer.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (s *server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/inprocess.Put"
	if err := valid.DirEntry(entry); err != nil {
		return nil, errors.E(op, err)
	}
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	canCreate, err := s.can(access.Create, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	canWrite, err := s.can(access.Write, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canCreate && !canWrite {
		return nil, errors.E(op, entry.Name, access.ErrPermissionDenied)
	}
	// If it doesn't exist, we need Create permission.
	if !canCreate {
		if _, err := s.lookup(op, parsed, true); err != nil { // TODO: Check exact error?
			// File does not exist but we do not have Create permission.
			return nil, errors.E(op, entry.Name, access.ErrPermissionDenied)
		}
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if access.IsAccessFile(entry.Name) || access.IsGroupFile(entry.Name) {
		if entry.Packing != upspin.PlainPack {
			return nil, errors.E(op, entry.Name, errors.Str("Access or Group file must use plain packing"))
		}
		if entry.IsLink() {
			return nil, errors.E(op, entry.Name, errors.Str("cannot create a link named Access or Group"))
		}
	}
	entry, err = s.put(op, entry, false)
	if err == nil {
		return nil, nil // Successful Put returns no entry.
	}
	return entry, err
}

// put is the underlying implementation of Put and MakeDirectory.
// If deleting, we expect the entry to already be present and skip it on the rewrite.
// TODO: implement links.
// TODO add Share?
func (s *server) put(op string, entry *upspin.DirEntry, deleting bool) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	pathName := parsed.Path()
	if parsed.IsRoot() {
		return nil, errors.E(op, pathName, errors.Errorf("cannot create root %s with Put; use MakeDirectory", parsed))
	}
	rootEntry, ok := s.db.root[parsed.User()]
	if !ok {
		// Cannot create user root with Put.
		return nil, errors.E(op, upspin.PathName(parsed.User()), errors.Str("no such user root"))
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
	var dirBlob []byte
	rootEntry, dirBlob, err = s.installEntry(op, path.DropPath(pathName, 1), rootEntry, entry, deleting, false)
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
			data, err := s.readAll(s.context, entry)
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
	const op = "dir/inprocess.WhichAccess"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// If the user has any right for this file, we can show the relevant Access file.
	canKnow, err := s.can(access.AnyRight, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canKnow {
		// Don't tell the user this path exists.
		return nil, errors.E(op, pathName, errors.NotExist)
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
// and returning a parsed access file.
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

// readAll retrieves the data for the entry.
func (s *server) readAll(context upspin.Context, entry *upspin.DirEntry) ([]byte, error) {
	return clientutil.ReadAll(context, entry)
}

// Delete implements upspin.DirServer.Delete.
func (s *server) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/inprocess.Delete"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	canDelete, err := s.can(access.Delete, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canDelete {
		return nil, errors.E(op, pathName, access.ErrPermissionDenied)
	}
	if parsed.IsRoot() {
		return nil, errors.E(op, pathName, errors.Str("cannot delete user root")) // Should be done in User service.
	}

	entry, err := s.lookup(op, parsed, false) // File must exist.
	if err != nil {
		return entry, err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	// If it is a directory, it must be empty.
	if entry.IsDir() {
		if !s.isEmptyDirectory(op, entry) {
			return nil, errors.E(op, pathName, errors.NotEmpty)
		}
	}

	return s.put(op, entry, true)
}

func (s *server) isEmptyDirectory(op string, entry *upspin.DirEntry) bool {
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
	const op = "dir/inprocess.Lookup"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	canRead, err := s.can(access.Read, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !canRead {
		return nil, errors.E(op, pathName, access.ErrPermissionDenied)
	}
	return s.lookup(op, parsed, true)
}

// lookup is the internal version of lookup; it does not do any Access checks.
func (s *server) lookup(op string, parsed path.Parsed, followFinal bool) (*upspin.DirEntry, error) {
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
			return nil, errors.E(op, parsed.Path(), errors.NotDir)
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
// TODO: Test access control for this method.
func (s *server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "dir/inprocess.Glob"
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, errors.E(op, err)
	}
	s.db.mu.RLock()
	rootEntry, ok := s.db.root[parsed.User()]
	s.db.mu.RUnlock()
	if !ok {
		return nil, errors.E(op, upspin.PathName(pattern), errors.NotExist, errors.Str("no such user"))
	}

	// Loop elementwise along the path, growing the list of candidates breadth-first.
	this := make([]*upspin.DirEntry, 0, 100)
	next := make([]*upspin.DirEntry, 1, 100)
	// Make placeholder entry for the root to bootstrap the loop. It doesn't need the block data.
	// Make a copy of the entry so we don't overwrite the root if we wipe the data before returning.
	e := *rootEntry
	next[0] = &e
	var links []*upspin.DirEntry // Will collect links we encounter.
	for i := 0; i < parsed.NElem(); i++ {
		elem := parsed.Elem(i)
		this, next = next, this[:0]
		// Invariant: There are no links in the list to be processed this iteration.
		for _, ent := range this {
			// ent must refer to a directory.
			if !ent.IsDir() {
				continue
			}
			// Need to check List permission.
			// Permission check is done for any intermediate step
			// (directory) if it's matched by a pattern,
			// and for the final entry always.
			if isGlobPattern(elem) || i == parsed.NElem()-1 {
				p, _ := path.Parse(ent.Name) // should always succeed
				if ok, err := s.can(access.List, p); err != nil {
					return nil, errors.E(op, upspin.PathName(pattern), err)
				} else if !ok {
					continue
				}
			}
			// Fetch the directory's contents.
			payload, err := s.readAll(s.db.dirContext, ent)
			if err != nil {
				return nil, errors.E(op, ent.Name, errors.Str("internal error: invalid reference: "+err.Error()))
			}
			for len(payload) > 0 {
				var nextEntry upspin.DirEntry
				remaining, err := nextEntry.Unmarshal(payload)
				if err != nil {
					return nil, errors.E(op, ent.Name, err)
				}
				payload = remaining
				nextParsed, err := path.Parse(nextEntry.Name)
				if err != nil {
					return nil, errors.E(op, ent.Name, err)
				}
				matched, err := goPath.Match(elem, nextParsed.Elem(nextParsed.NElem()-1))
				if err != nil {
					return nil, errors.E(op, upspin.PathName(pattern), errors.Syntax, err)
				}
				if !matched {
					continue
				}
				// Do not return ErrFollowLink if the link is the last element. The result
				// of the glob operation is the link itself in that case.
				if i != parsed.NElem()-1 && nextEntry.IsLink() {
					// Remove it from consideration. Will be restored after the loop.
					links = append(links, &nextEntry)
				} else {
					next = append(next, &nextEntry)
				}
			}
		}
	}

	// Now iterate over the parsed entries and clear out the location
	// information for entries we can't read.
	// We only need do the check once per directory.

	// checked and canRead apply to all files in checkedPrefix.
	var checked, canRead bool
	var checkedPrefix upspin.PathName

	next = append(next, links...)
	upspin.SortDirEntries(next, false)
	for _, entry := range next {
		parsed, _ := path.Parse(entry.Name) // should always work
		if parent := parsed.Drop(1).Path(); !parsed.IsRoot() && checkedPrefix != parent {
			checkedPrefix = parent
			checked = false
		}
		if !checked {
			canRead, _ = s.can(access.Read, parsed)
			checked = true
		}
		if !canRead {
			entry.Blocks = nil
			entry.Packdata = nil
		}
	}

	if len(links) > 0 {
		return next, upspin.ErrFollowLink
	}
	return next, nil
}

func isGlobPattern(elem string) bool {
	return strings.ContainsAny(elem, `*?[]`)
}

// can reports whether the calling user (defined by s.context.UserName()) has the
// access right for this file or directory.
// s.db.mu is _not_ held.
func (s *server) can(right access.Right, parsed path.Parsed) (bool, error) {
	accessFile := s.whichAccess(parsed)
	if accessFile == nil {
		accessFile = s.rootAccessFile(parsed)
	}
	return accessFile.Can(s.context.UserName(), right, parsed.Path(), s.load)
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
	return s.readAll(s.context, entry)
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
func (s *server) fetchEntry(op string, entry *upspin.DirEntry, elem string) (*upspin.DirEntry, error) {
	payload, err := s.readAll(s.db.dirContext, entry)
	if err != nil {
		return nil, err
	}
	return s.dirEntLookup(op, entry.Name, payload, elem)
}

// dirEntLookup returns the ref for the entry in the named directory whose contents are given in the payload.
// The boolean is true if the entry itself describes a directory.
func (s *server) dirEntLookup(op string, pathName upspin.PathName, payload []byte, elem string) (*upspin.DirEntry, error) {
	if len(elem) == 0 {
		return nil, errors.E(op, pathName, errors.E("empty path name element"))
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
// entry as required. It returns the entry updated directory and the blob itself.
func (s *server) installEntry(op string, dirName upspin.PathName, dirEntry *upspin.DirEntry, newEntry *upspin.DirEntry, deleting, dirOverwriteOK bool) (*upspin.DirEntry, []byte, error) {
	dirData, err := s.readAll(s.db.dirContext, dirEntry)
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
				return nil, nil, errors.E(op, errors.IsDir, dirName, errors.Str("cannot overwrite directory"))
			}
		}
		// Drop this entry so we can append the updated one (or skip it, if we're deleting).
		// It may have changed length because of the metadata being unpredictable,
		// so we cannot overwrite it in place.
		copy(dirData[start:], remaining)
		dirData = dirData[:len(dirData)-length]
		if !deleting {
			// We want nextEntry's sequence (previous value+1) but everything else from newEntry.
			if newEntry.Sequence != upspin.SeqIgnore {
				if newEntry.Sequence != nextEntry.Sequence {
					return nil, nil, errors.E(op, newEntry.Name, errSeq)
				}
			}
			newEntry.Sequence = nextEntry.Sequence + 1
		}
		break
	}
	if deleting {
		// Must exist.
		if !found {
			return nil, nil, errors.E(op, newEntry.Name, errors.NotExist)
		}
	} else {
		// Add new entry to directory.
		data, err := newEntry.Marshal()
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		dirData = append(dirData, data...)
	}
	entry, err := s.newDirEntry(dirName, dirData, 0)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}
	return entry, dirData, nil
}

// DeleteAll implements upspin.DirServer.DeleteAll.
func (s *server) DeleteAll() {
	s.db.mu.Lock()
	s.db.root = make(map[upspin.UserName]*upspin.DirEntry)
	s.db.access = make(map[upspin.PathName]*access.Access)
	s.db.mu.Unlock()
}

// Methods to implement upspin.Dialer.

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (s *server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "dir/inprocess.Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if !s.db.dialed {
		if context.UserName() == "" {
			return nil, errors.E(op, errors.Invalid, errors.Str("no user name"))
		}
		// This is the first call; set the owner and endpoint.
		s.db.dirContext = context
		s.db.dialed = true
	}
	this := *s // Make a copy.
	this.context = context
	return &this, nil
}

// Endpoint implements upspin.DirServer.Endpoint.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // Ignored.
	}
}

// Ping implements upspin.DirServer.Ping.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.server.
func (s *server) Close() {
	// TODO
}

// Authenticate implements upspin.Service.
func (s *server) Authenticate(upspin.Context) error {
	// TODO
	return nil
}
