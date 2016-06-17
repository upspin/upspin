// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple, non-persistent, in-memory directory service.
package inprocess

import (
	"fmt"
	goPath "path"
	"sort"
	"strings"
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"

	// Imported because it's used to pack dir entries.
	_ "upspin.io/pack/plain"
)

// Used to store directory entries.
// All directories are encoded with this packing; the user-created
// blobs are packed according to the arguments to Put.
var (
	dirPacking = upspin.PlainPack
)

var (
	loc0 upspin.Location
)

// Service implements directories and file-level I/O.
type Service struct {
	upspin.NoConfiguration
	// context holds the context that created the call.
	context upspin.Context
	db      *database
}

type database struct {
	endpoint upspin.Endpoint

	dirContext *upspin.Context // For accessing store holding directory entries.

	// mu is used to serialize access to the maps.
	// It's also used to serialize all access to the store through the
	// exported API, for simple but slow safety. At least it's an RWMutex
	// so it's not _too_ bad.
	mu sync.RWMutex

	// serviceOwner identifies the user running the user service. TODO: unused.
	serviceOwner upspin.UserName

	// root stores the directory entry for each user's root.
	root map[upspin.UserName]*upspin.DirEntry

	// rootAccess stores the default Access file for each user's root.
	// Computed lazily and only used if needed.
	rootAccess map[upspin.UserName]*access.Access

	// access stores the parsed contents of any Access file stored
	// in this directory. Inherited rights are computed from this map.
	access map[upspin.PathName]*access.Access
}

var _ upspin.Directory = (*Service)(nil)

func newDirEntry(context *upspin.Context, name upspin.PathName) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name: name,
		Location: upspin.Location{
			Endpoint: context.Store,
		},
		Metadata: upspin.Metadata{
			Packdata: upspin.Packdata{byte(dirPacking)},
		},
	}
}

func (db *database) packDirBlob(op string, cleartext []byte, name upspin.PathName) ([]byte, *upspin.Metadata, error) {
	return packBlob(db.dirContext, op, cleartext, newDirEntry(db.dirContext, name))
}

func getPacker(packdata upspin.Packdata) (upspin.Packer, error) {
	if len(packdata) == 0 {
		return nil, errors.Str("no packdata")
	}
	packer := pack.Lookup(upspin.Packing(packdata[0]))
	if packer == nil {
		return nil, fmt.Errorf("no packing %#x registered", packdata[0])
	}
	return packer, nil
}

// packBlob packs an arbitrary blob and its metadata.
func packBlob(context *upspin.Context, op string, cleartext []byte, entry *upspin.DirEntry) ([]byte, *upspin.Metadata, error) {
	packer, err := getPacker(entry.Metadata.Packdata)
	if err != nil {
		return nil, nil, errors.E(op, entry.Name, errors.Invalid, err)
	}
	cipherLen := packer.PackLen(context, cleartext, entry)
	if cipherLen < 0 {
		return nil, nil, errors.E(op, err)
	}
	ciphertext := make([]byte, cipherLen)
	n, err := packer.Pack(context, ciphertext, cleartext, entry)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}
	return ciphertext[:n], &entry.Metadata, nil
}

// unpackBlob unpacks a blob.
// Other than from unpackDirBlob, only used in tests.
func unpackBlob(context *upspin.Context, op string, ciphertext []byte, entry *upspin.DirEntry) ([]byte, error) {
	packer, err := getPacker(entry.Metadata.Packdata)
	if err != nil {
		return nil, errors.E(op, entry.Name, errors.Invalid, err)
	}
	clearLen := packer.UnpackLen(context, ciphertext, entry)
	if clearLen < 0 {
		return nil, errors.E(op, err)
	}
	cleartext := make([]byte, clearLen)
	n, err := packer.Unpack(context, cleartext, ciphertext, entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return cleartext[:n], nil
}

// unpackDirBlob unpacks a blob that is known to be a directory record.
func unpackDirBlob(context *upspin.Context, op string, ciphertext []byte, name upspin.PathName) ([]byte, error) {
	return unpackBlob(context, op, ciphertext, newDirEntry(context, name))
}

// Glob implements upspin.Directory.Glob.
// TODO: Test access control for this method.
func (s *Service) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const Glob = "Glob"
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, errors.E(Glob, err)
	}
	s.db.mu.RLock()
	dirEntry, ok := s.db.root[parsed.User()]
	s.db.mu.RUnlock()
	if !ok {
		return nil, errors.E(Glob, upspin.PathName(pattern), "no such user")
	}
	// Check if pattern is a valid go path pattern
	_, err = goPath.Match(parsed.FilePath(), "")
	if err != nil {
		return nil, errors.E(Glob, upspin.PathName(pattern), err)
	}

	dirRef := dirEntry.Location.Reference
	// Loop elementwise along the path, growing the list of candidates breadth-first.
	this := make([]*upspin.DirEntry, 0, 100)
	next := make([]*upspin.DirEntry, 1, 100)
	next[0] = &upspin.DirEntry{
		Name: parsed.First(0).Path(), // The root.
		Location: upspin.Location{
			Reference: dirRef,
			Endpoint:  s.context.Store,
		},
		Metadata: upspin.Metadata{
			Attr: upspin.AttrDirectory,
		},
	}
	for i := 0; i < parsed.NElem(); i++ {
		elem := parsed.Elem(i)
		// Need to check List permission. Permission check is done for any
		// intermediate step (directory) if it's matched by a pattern, and for the final
		// entry always.
		if isGlobPattern(elem) || i == parsed.NElem()-1 {
			ok, err := s.can(access.List, parsed.First(i))
			if err != nil {
				return nil, errors.E(Glob, upspin.PathName(pattern), err)
			}
			if !ok {
				return nil, errors.E(Glob, upspin.PathName(pattern), access.ErrPermissionDenied)
			}
		}
		this, next = next, this[:0]
		for _, ent := range this {
			// ent must refer to a directory.
			if !ent.IsDir() {
				continue
			}
			s.db.mu.RLock()
			payload, err := s.fetchDir(Glob, ent.Location.Reference, ent.Name)
			s.db.mu.RUnlock()
			if err != nil {
				return nil, errors.E(Glob, ent.Name, errors.Str("internal error: invalid reference: "+err.Error()))
			}
			for len(payload) > 0 {
				var nextEntry upspin.DirEntry
				remaining, err := nextEntry.Unmarshal(payload)
				if err != nil {
					return nil, errors.E(Glob, ent.Name, err)
				}
				payload = remaining
				parsed, err := path.Parse(nextEntry.Name)
				if err != nil {
					return nil, errors.E(Glob, ent.Name, err)
				}
				// No need to check error; pattern is validated above.
				if matched, _ := goPath.Match(elem, parsed.Elem(parsed.NElem()-1)); !matched {
					continue
				}
				next = append(next, &nextEntry)
			}
		}
	}
	var checked, canRead bool
	for _, entry := range next {
		// Need a / on the root if it's matched.
		if entry.Name == upspin.PathName(parsed.User()) {
			entry.Name += "/"
		}
		// Clear out the location information if we can't read this.
		// All will be in the same directory so we only need to check one.
		if !checked {
			parsed, err := path.Parse(entry.Name)
			if err != nil {
				canRead, _ = s.can(access.Read, parsed)
			}
			checked = true
		}
		if !canRead {
			entry.Location = loc0
			entry.Metadata.Packdata = nil
		}
	}
	sort.Sort(dirEntrySlice(next))

	return next, nil
}

func isGlobPattern(elem string) bool {
	return strings.ContainsAny(elem, `*?[]`)
}

// For sorting.
type dirEntrySlice []*upspin.DirEntry

func (d dirEntrySlice) Len() int           { return len(d) }
func (d dirEntrySlice) Less(i, j int) bool { return d[i].Name < d[j].Name }
func (d dirEntrySlice) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }

func (s *Service) rootDirEntry(user upspin.UserName, ref upspin.Reference, seq int64) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name: upspin.PathName(user + "/"),
		Location: upspin.Location{
			Endpoint:  s.context.Store,
			Reference: ref,
		},
		Metadata: upspin.Metadata{
			Attr:     upspin.AttrDirectory,
			Sequence: seq,
			Size:     0,
			Time:     upspin.Now(),
			Packdata: upspin.Packdata{byte(dirPacking)},
		},
	}
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (s *Service) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	const MakeDirectory = "MakeDirectory"
	log.Printf("directory/inprocess MakeDirectory %q", directoryName)
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	parsed, err := path.Parse(directoryName)
	if err != nil {
		return loc0, errors.E(MakeDirectory, err)
	}
	canCreate, err := s.can(access.Create, parsed)
	if err != nil {
		return loc0, errors.E(MakeDirectory, err)
	}
	if !canCreate {
		return loc0, errors.E(MakeDirectory, directoryName, access.ErrPermissionDenied)
	}
	pathName := parsed.Path()
	if access.IsAccessFile(pathName) || access.IsGroupFile(pathName) {
		return loc0, errors.E(MakeDirectory, directoryName, errors.Str("cannot create directory named Access or Group"))
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	store, err := bind.Store(&s.context, s.context.Store)
	if err != nil {
		return loc0, errors.E(MakeDirectory, directoryName, err)
	}
	if parsed.IsRoot() {
		// Creating a root: easy!
		// Only the onwer can create the root, but the check above is sufficient since a
		// non-existent root has no Access file yet.
		if _, present := s.db.root[parsed.User()]; present {
			return loc0, errors.E("MakeDirectory", directoryName, errors.Exist)
		}
		blob, _, err := s.db.packDirBlob(MakeDirectory, nil, pathName) // TODO: Ignoring metadata (but using PlainPack).
		if err != nil {
			return loc0, err
		}
		ref, err := store.Put(blob)
		if err != nil {
			return loc0, errors.E(MakeDirectory, directoryName, err)
		}
		dirEntry := s.rootDirEntry(parsed.User(), ref, 0)
		s.db.root[parsed.User()] = dirEntry
		return dirEntry.Location, nil
	}
	// Use parsed.Path() rather than directoryName so it's canonicalized.
	ref, err := store.Put([]byte{}) // Nothing to store, but we need a reference.
	if err != nil {
		return loc0, errors.E(MakeDirectory, directoryName, err)
	}
	loc := upspin.Location{
		Endpoint:  s.context.Store,
		Reference: ref,
	}
	entry := newDirEntry(&s.context, parsed.Path())
	entry.SetDir()
	entry.Location = loc
	return loc, s.put(MakeDirectory, entry, false)
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (s *Service) Put(entry *upspin.DirEntry) error {
	const Put = "Put"
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return errors.E(Put, err)
	}
	// Use the clean name, in case the caller forgot.
	entry.Name = parsed.Path()
	canCreate, err := s.can(access.Create, parsed)
	if err != nil {
		return errors.E(Put, err)
	}
	canWrite, err := s.can(access.Write, parsed)
	if err != nil {
		return errors.E(Put, err)
	}
	if !canCreate && !canWrite {
		return errors.E(Put, entry.Name, access.ErrPermissionDenied)
	}
	// If it doesn't exist, we need Create permission.
	if !canCreate {
		if _, err := s.lookup(Put, parsed); err != nil { // TODO: Check exact error?
			// File does not exist but we do not have Create permission.
			return errors.E(Put, entry.Name, access.ErrPermissionDenied)
		}
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if access.IsAccessFile(entry.Name) || access.IsGroupFile(entry.Name) {
		if entry.Metadata.Packing() != upspin.PlainPack {
			return errors.E(Put, entry.Name, errors.Str("Access or Group file must use plain packing"))
		}
	}
	err = s.put(Put, entry, false)
	if err != nil {
		return err
	}
	// Put was successful. If it was an Access or Group file, there's more to do.
	if access.IsAccessFile(entry.Name) || access.IsGroupFile(entry.Name) {
		if access.IsGroupFile(entry.Name) {
			// Group files are loaded on demand but we must wipe the cache.
			access.RemoveGroup(entry.Name)
		}
		if access.IsAccessFile(entry.Name) {
			data, err := s.getData(entry)
			if err != nil {
				return errors.E(Put, err)
			}
			accessFile, err := access.Parse(entry.Name, data)
			if err != nil {
				return errors.E(Put, err)
			}
			s.db.access[path.DropPath(entry.Name, 1)] = accessFile
		}
	}
	return nil
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (s *Service) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	const WhichAccess = "WhichAccess"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return "", errors.E(WhichAccess, err)
	}
	accessFile := s.whichAccess(parsed)
	if accessFile == nil {
		return "", nil
	}
	return accessFile.Path(), nil
}

// whichAccess is the workings of WhichAccess, accepting a parsed path name
// and returning a parsed access file.
func (s *Service) whichAccess(parsed path.Parsed) *access.Access {
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
		// Step up to parent directory.
		parsed = parsed.Drop(1)
	}
}

// put is the underlying implementation of Put and MakeDirectory.
// If deleting, we expect the entry to already be present and skip it on the rewrite.
// TODO add Share?
func (s *Service) put(op string, entry *upspin.DirEntry, deleting bool) error {
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return errors.E(op, err)
	}
	pathName := parsed.Path()
	if parsed.IsRoot() {
		return errors.E(op, pathName, errors.Str("cannot create root with Put; use MakeDirectory"))
	}
	dirEntry, ok := s.db.root[parsed.User()]
	if !ok {
		// Cannot create user root with Put.
		return errors.E(op, upspin.PathName(parsed.User()), errors.Str("no such user"))
	}
	dirRef := dirEntry.Location.Reference
	// Iterate along the path up to but not past the last element.
	// We remember the entries as we descend for fast(er) overwrite of the Merkle tree.
	// Invariant: dirRef refers to a directory.
	entries := make([]*upspin.DirEntry, 0, 10) // 0th entry is the root.
	entries = append(entries, dirEntry)
	for i := 0; i < parsed.NElem()-1; i++ {
		e, err := s.fetchEntry(op, parsed.First(i).Path(), dirRef, parsed.Elem(i))
		if err != nil {
			return err
		}
		if !e.IsDir() {
			return errors.E(op, parsed.First(i+1).Path(), errors.Str("not a directory"))
		}
		entries = append(entries, e)
		dirRef = e.Location.Reference
	}
	dirRef, err = s.installEntry(op, path.DropPath(pathName, 1), dirRef, entry, deleting, false)
	if err != nil {
		// TODO: System is now inconsistent.
		return err
	}
	// Rewrite the tree up to the root.
	// Invariant: dirRef identifies the directory that has just been updated.
	// i indicates the directory that needs to be updated to store the new dirRef.
	for i := len(entries) - 2; i >= 0; i-- {
		// Install into the ith directory the (i+1)th entry.
		dirEntry := &upspin.DirEntry{
			Name: entries[i+1].Name,
			Location: upspin.Location{
				Endpoint:  s.context.Store,
				Reference: dirRef,
			},
			Metadata: upspin.Metadata{
				Attr:     upspin.AttrDirectory,
				Sequence: entries[i+1].Metadata.Sequence,
				Packdata: upspin.Packdata{byte(dirPacking)},
			},
		}
		dirRef, err = s.installEntry(op, parsed.First(i).Path(), entries[i].Location.Reference, dirEntry, false, true)
		if err != nil {
			// TODO: System is now inconsistent.
			return err
		}
	}
	// Update the root.
	seq := s.db.root[parsed.User()].Metadata.Sequence
	s.db.root[parsed.User()] = s.rootDirEntry(parsed.User(), dirRef, seq+1)

	return nil
}

// getData retrieves the data for the entry. s.db.mu is held for write.
func (s *Service) getData(entry *upspin.DirEntry) ([]byte, error) {
	store, err := bind.Store(&s.context, entry.Location.Endpoint)
	if err != nil {
		return nil, err
	}
	data, _, err := store.Get(entry.Location.Reference)
	if err != nil {
		// TODO: Should handle redirection.
		return nil, err
	}
	return data, err
}

// Delete implements upspin.Directory.Delete.
func (s *Service) Delete(pathName upspin.PathName) error {
	const Delete = "Delete"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return errors.E(Delete, err)
	}
	canDelete, err := s.can(access.Delete, parsed)
	if err != nil {
		return errors.E(Delete, err)
	}
	if !canDelete {
		return errors.E(Delete, pathName, access.ErrPermissionDenied)
	}
	if parsed.IsRoot() {
		return errors.E(Delete, pathName, errors.Str("cannot delete user root")) // Should be done in User service.
	}

	entry, err := s.lookup(Delete, parsed) // File must exist.
	if err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	// If it is a directory, it must be empty.
	if entry.IsDir() {
		empty, err := s.isEmptyDirectory(Delete, entry)
		if err != nil {
			return errors.E(Delete, err)
		}
		if !empty {
			return errors.E(Delete, pathName, errors.Str("directory not empty"))
		}
	}

	return s.put(Delete, entry, true)
}

func (s *Service) isEmptyDirectory(op string, entry *upspin.DirEntry) (bool, error) {
	data, err := s.fetchDir(op, entry.Location.Reference, entry.Name)
	if err != nil {
		return false, err
	}
	return len(data) == 0, nil
}

// Lookup implements upspin.Directory.Lookup.
func (s *Service) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const Lookup = "Lookup"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(Lookup, err)
	}
	canRead, err := s.can(access.Read, parsed)
	if err != nil {
		return nil, errors.E(Lookup, err)
	}
	if !canRead {
		return nil, errors.E(Lookup, pathName, access.ErrPermissionDenied)
	}
	entry, err := s.lookup(Lookup, parsed)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// lookup is the internal version of lookup; it does not do any Access checks.
func (s *Service) lookup(op string, parsed path.Parsed) (*upspin.DirEntry, error) {
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	dirEntry, ok := s.db.root[parsed.User()]
	if !ok {
		return nil, errors.E(upspin.PathName(parsed.User()), "no such user")
	}
	if parsed.IsRoot() {
		return dirEntry, nil
	}
	dirRef := dirEntry.Location.Reference
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	for i := 0; i < parsed.NElem()-1; i++ {
		entry, err := s.fetchEntry(op, parsed.First(i).Path(), dirRef, parsed.Elem(i))
		if err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			return nil, errors.E(op, parsed.Path(), errors.E("not a directory"))
		}
		dirRef = entry.Location.Reference
	}
	lastElem := parsed.Elem(parsed.NElem() - 1)
	// Destination must exist. If so we need to update the parent directory record.
	entry, err := s.fetchEntry(op, parsed.Drop(1).Path(), dirRef, lastElem)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// can reports whether the calling user (defined by s.context.UserName) has the
// access right for this file or directory.
// s.db.mu is _not_ held.
func (s *Service) can(right access.Right, parsed path.Parsed) (bool, error) {
	accessFile := s.whichAccess(parsed)
	if accessFile == nil {
		accessFile = s.rootAccessFile(parsed)
	}
	return accessFile.Can(s.context.UserName, right, parsed.Path(), s.load)
}

// load is a helper for Access.Can that gets the entire contents of the named item.
func (s *Service) load(name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	entry, err := s.lookup("access", parsed)
	if err != nil {
		return nil, err
	}
	return s.getData(entry)
}

// rootAccess file returns the parsed Access file providing default permissions for the root of this path.
func (s *Service) rootAccessFile(parsed path.Parsed) *access.Access {
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

// fetchEntry returns the reference for the named elem within the named directory referenced by dirRef.
// It reads the whole directory, so avoid calling it repeatedly.
func (s *Service) fetchEntry(op string, name upspin.PathName, dirRef upspin.Reference, elem string) (*upspin.DirEntry, error) {
	payload, err := s.fetchDir(op, dirRef, name)
	if err != nil {
		return nil, err
	}
	return s.dirEntLookup(op, name, payload, elem)
}

// fetchDir returns the decrypted directory data associated with the reference.
func (s *Service) fetchDir(op string, dirRef upspin.Reference, name upspin.PathName) ([]byte, error) {
	store, err := bind.Store(&s.context, s.context.Store)
	if err != nil {
		return nil, err
	}
	ciphertext, locs, err := store.Get(dirRef)
	if err != nil {
		return nil, err
	}
	// TODO: this only works for one redirect.
	if locs != nil {
		ciphertext, _, err = store.Get(locs[0].Reference)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}
	payload, err := unpackDirBlob(s.db.dirContext, op, ciphertext, name)
	return payload, err
}

// dirEntLookup returns the ref for the entry in the named directory whose contents are given in the payload.
// The boolean is true if the entry itself describes a directory.
func (s *Service) dirEntLookup(op string, pathName upspin.PathName, payload []byte, elem string) (*upspin.DirEntry, error) {
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
	return nil, errors.E(op, pathName, errors.Str("no such directory entry: "+elem))
}

var errSeq = errors.Str("sequence mismatch")

// installEntry installs the new entry in the directory referenced by dirLeu, appending or overwriting the
// entry as required. It returns the ref for the updated directory.
func (s *Service) installEntry(op string, dirName upspin.PathName, dirRef upspin.Reference, newEntry *upspin.DirEntry, deleting, dirOverwriteOK bool) (upspin.Reference, error) {
	if dirRef == "" {
		panic("nothing")
	}
	dirData, err := s.fetchDir(op, dirRef, dirName)
	if err != nil {
		return "", err
	}
	found := false
	var nextEntry upspin.DirEntry
	for payload := dirData; len(payload) > 0; {
		// Remember where this entry starts.
		start := len(dirData) - len(payload)
		remaining, err := nextEntry.Unmarshal(payload)
		if err != nil {
			return "", errors.E(op, err)
		}
		length := len(payload) - len(remaining)
		payload = remaining
		if nextEntry.Name != newEntry.Name {
			continue
		}
		// We found the item with that name.
		found = true
		if !deleting {
			// If it's already there and the sequence number is SeqNotExist, this is an error.
			if newEntry.Metadata.Sequence == upspin.SeqNotExist {
				return "", errors.E(op, newEntry.Name, errors.Exist)
			}
			// If it's already there and is not expected to be a directory, this is an error.
			if nextEntry.IsDir() && !dirOverwriteOK {
				return "", errors.E(op, dirName, errors.Str("cannot overwrite directory"))
			}
		}
		// Drop this entry so we can append the updated one (or skip it, if we're deleting).
		// It may have changed length because of the metadata being unpredictable,
		// so we cannot overwrite it in place.
		copy(dirData[start:], remaining)
		dirData = dirData[:len(dirData)-length]
		if !deleting {
			// We want nextEntry's sequence (previous value+1) but everything else from newEntry.
			if newEntry.Metadata.Sequence != upspin.SeqIgnore {
				if newEntry.Metadata.Sequence != nextEntry.Metadata.Sequence {
					return "", errors.E(op, newEntry.Name, errSeq)
				}
			}
			newEntry.Metadata.Sequence = nextEntry.Metadata.Sequence + 1
		}
		break
	}
	if deleting {
		// Must exist.
		if !found {
			return "", errors.E(op, newEntry.Name, errors.NotExist)
		}
	} else {
		// Add new entry to directory.
		data, err := newEntry.Marshal()
		if err != nil {
			return "", errors.E(op, err)
		}
		dirData = append(dirData, data...)
	}
	blob, _, err := s.db.packDirBlob(op, dirData, dirName) // TODO: Ignoring metadata (but using PlainPack).
	store, err := bind.Store(&s.context, s.context.Store)
	if err != nil {
		return "", errors.E(op, err)
	}
	ref, err := store.Put(blob)
	if err != nil {
		// TODO: System is now inconsistent.
		return "", errors.E(op, err)
	}
	return ref, nil
}

// DeleteAll implements upspin.Directory.DeleteAll.
func (s *Service) DeleteAll() {
	s.db.mu.Lock()
	s.db.root = make(map[upspin.UserName]*upspin.DirEntry)
	s.db.access = make(map[upspin.PathName]*access.Access)
	s.db.mu.Unlock()
}

// Methods to implement upspin.Dialer.

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (s *Service) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const Dial = "Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(Dial, errors.Invalid, errors.Str("unrecognized transport"))
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.serviceOwner == "" {
		if context.UserName == "" {
			return nil, errors.E(Dial, errors.Invalid, errors.Str("no user name"))
		}
		// This is the first call; set the owner and endpoint.
		s.db.endpoint = e
		s.db.dirContext = context
		s.db.serviceOwner = context.UserName
	}
	this := *s // Make a copy.
	this.context = *context
	return &this, nil
}

// Endpoint implements upspin.Directory.Endpoint.
func (s *Service) Endpoint() upspin.Endpoint {
	return s.db.endpoint
}

// Ping implements upspin.Directory.Ping.
func (s *Service) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *Service) Close() {
	// TODO
}

// Authenticate implements upspin.Service.
func (s *Service) Authenticate(*upspin.Context) error {
	// TODO
	return nil
}

const transport = upspin.InProcess

func init() {
	s := &Service{
		db: &database{
			endpoint: upspin.Endpoint{
				Transport: upspin.InProcess,
				NetAddr:   "", // Ignored.
			},
			root:       make(map[upspin.UserName]*upspin.DirEntry),
			rootAccess: make(map[upspin.UserName]*access.Access),
			access:     make(map[upspin.PathName]*access.Access),
		},
	}
	bind.RegisterDirectory(transport, s)
}
