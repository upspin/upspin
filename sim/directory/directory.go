// Package directory implements the directory service for the simulator.
package directory

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"

	"upspin.googlesource.com/upspin.git/sim/path"
	"upspin.googlesource.com/upspin.git/sim/ref"
	"upspin.googlesource.com/upspin.git/sim/store"
)

var (
	r0  ref.Reference
	hr0 ref.HintedReference
)

// Service implements directories and file-level I/O.
type Service struct {
	StoreLocation ref.Location
	Store         *store.Service
	Root          map[path.UserName]ref.Reference // TODO. No need for hint, they're all on ds.Store.
}

// NewService returns a new, empty directory server that will store its data in the specified store service.
func NewService(ss *store.Service) *Service {
	return &Service{
		StoreLocation: ss.Location,
		Store:         ss,
		Root:          make(map[path.UserName]ref.Reference),
	}
}

// Entry represents the metadata for a file in a directory.
type Entry struct {
	Elem  string        // Path element, such as "foo" representing the file a@b.c/a/b/c/foo.
	IsDir bool          // The referenced item is itself a directory.
	ref   ref.Reference // Not hinted, so replicas hold the same data. Directories are near blob servers.
}

// mkError creates an os.PathError from the arguments.
func mkError(op string, name path.Name, err string) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(name),
		Err:  errors.New(err),
	}
}

func (s *Service) Lookup(pathName path.Name) (ref.HintedReference, error) {
	return hr0, nil
}

func (s *Service) Glob(pattern string) ([]Entry, error) {
	return nil, nil
}

// MakeDirectory creates a new directory with the given name. The user's root must be present.
// TODO: For now at least, only the last entry of the path can be created, as in Unix.
// TODO: We get the reference back. Should we be able to use it instead of a path for Put?
// That Would require more self-checks on directories (easy) and would avoid some name lookup (good)
// but is lower-level. Maybe as an efficiency extra in the API.
func (s *Service) MakeDirectory(directoryName path.Name) (ref.HintedReference, error) {
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	parsed, err := path.Parse(directoryName + "/")
	if err != nil {
		return hr0, nil
	}
	if len(parsed.Elems) == 0 {
		// Creating a root: easy!
		if _, present := s.Root[parsed.User]; present {
			return hr0, mkError("MakeDirectory", directoryName, "already exists")
		}
		blob := store.MakeBlob(parsed.String(), nil)
		r, err := s.Store.Put(blob)
		if err != nil {
			return hr0, nil
		}
		href := ref.HintedReference{
			Reference: ref.Reference{r.Hash},
			Location:  s.StoreLocation,
		}
		s.Root[parsed.User] = href.Reference
		return href, nil
	}
	blob := store.MakeBlob(parsed.String(), nil)
	r, err := s.Store.Put(blob)
	if err != nil {
		return hr0, nil
	}
	data := newEntryBytes(parsed.Elems[len(parsed.Elems)-1], true, r)
	return s.put("MakeDirectory", directoryName, true, data)
}

// Put creates or overwrites the blob with the specified path.
// The path begins with the user name (which contains no slashes),
// always followed by at least one slash:
//	gopher@google.com/
//	gopher@google.com/a/b/c
// Directories are created with MakeDir. Roots are anyway. TODO.
func (s *Service) Put(pathName path.Name, data []byte) (ref.HintedReference, error) {
	return s.put("Put", pathName, false, data)
}

// put is the underlying implementation of Put and MakeDirectory.
func (s *Service) put(op string, pathName path.Name, dataIsDir bool, data []byte) (ref.HintedReference, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return hr0, nil
	}
	if len(parsed.Elems) == 0 {
		return hr0, mkError(op, pathName, "cannot create root with Put; use MakeDirectory")
	}
	dirRef, ok := s.Root[parsed.User]
	if !ok {
		// Cannot create user root with Put.
		return hr0, mkError(op, path.Name(parsed.User), "no such user")
	}
	// Iterate along the path up to but not past the last element.
	// We remember the entries as we descend for fast(er) overwrite of the Merkle tree.
	// Invariant: dirRef refers to a directory.
	isDir := true
	entries := make([]Entry, 0, 10) // 0th entry is the root.
	entries = append(entries, Entry{"", true, dirRef})
	for i := 0; i < len(parsed.Elems)-1; i++ {
		elem := parsed.Elems[i]
		dirRef, isDir, err = s.fetchEntry("Put", pathName, dirRef, parsed.Elems[i])
		if err != nil {
			return hr0, err
		}
		if !isDir {
			return hr0, mkError(op, parsed.First(i+1).Path(), "not a directory")
		}
		entries = append(entries, Entry{elem, true, dirRef}) // TODO: IsDir should be checked
	}
	lastElem := parsed.Elems[len(parsed.Elems)-1]

	// Create a blob storing the data for this file and store it in storage service.
	ciphertext := store.MakeBlob(string(pathName), data)
	r, err := s.Store.Put(ciphertext)
	// TODO VALIDATE REF

	// Update directory holding the file. TODO: must be atomic.
	// Need the name of the directory we're updating.
	dirRef, err = s.installEntry(op, parsed.Drop(1).String(), dirRef, Entry{lastElem, dataIsDir, r}, false)
	if err != nil {
		// TODO: System is now inconsistent.
		return hr0, err
	}
	// Rewrite the tree up to the root.
	// Invariant: dirRef identifies the directory that has just been updated.
	// i indicates the directory that needs to be updated to store the new dirRef.
	for i := len(entries) - 2; i >= 0; i-- {
		// Install into the ith directory the (i+1)th entry.
		dirRef, err = s.installEntry(op, parsed.String(), entries[i].ref, Entry{entries[i+1].Elem, true, dirRef}, true)
		if err != nil {
			// TODO: System is now inconsistent.
			return hr0, err
		}
	}
	// Update the root.
	s.Root[parsed.User] = dirRef
	href := ref.HintedReference{
		Reference: r,
		Location:  s.StoreLocation,
	}
	return href, nil
}

func (s *Service) Get(pathName path.Name) (ref.HintedReference, []byte, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return hr0, nil, nil
	}
	if len(parsed.Elems) == 0 {
		return hr0, nil, mkError("Get", pathName, "cannot use Get on directory; use Glob")
	}
	dirRef, ok := s.Root[parsed.User]
	if !ok {
		return hr0, nil, mkError("Get", path.Name(parsed.User), "no such user")
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(parsed.Elems)-1; i++ {
		dirRef, isDir, err = s.fetchEntry("Get", pathName, dirRef, parsed.Elems[i])
		if err != nil {
			return hr0, nil, nil
		}
		if !isDir {
			return hr0, nil, mkError("Get", pathName, "not a directory")
		}
	}
	lastElem := parsed.Elems[len(parsed.Elems)-1]
	// Destination must exist. If so we need to update the parent directory record.
	var r ref.Reference
	if r, isDir, err = s.fetchEntry("Get", pathName, dirRef, lastElem); err != nil {
		return hr0, nil, err
	}
	if isDir {
		return hr0, nil, mkError("Get", pathName, "is a directory")
	}
	ciphertext, err := s.Store.Get(r)
	if err != nil {
		return hr0, nil, &os.PathError{"Get", string(pathName), err}
	}
	name, cleartext, err := store.UnpackBlob(ciphertext)
	if err != nil {
		return hr0, nil, &os.PathError{"Get", string(pathName), err}
	}
	// TODO: Check name.
	_ = name
	href := ref.HintedReference{
		Reference: r,
		Location:  s.StoreLocation,
	}
	return href, cleartext, nil
}

func newEntryBytes(elem string, isDir bool, ref ref.Reference) []byte {
	entry := make([]byte, 0, 1+len(elem)+1+sha1.Size)
	entry = append(entry, byte(len(elem)))
	entry = append(entry, elem...)
	dirByte := byte(0)
	if isDir {
		dirByte = 1
	}
	entry = append(entry, dirByte)
	entry = append(entry, ref.Hash[:]...)
	return entry
}

// fetchEntry returns the reference for the named elem within the named directory referenced by dirRef.
// It reads the whole directory, so avoid calling it repeatedly.
func (s *Service) fetchEntry(op string, name path.Name, dirRef ref.Reference, elem string) (ref.Reference, bool, error) {
	payload, err := s.fetch(dirRef)
	if err != nil {
		return r0, false, err
	}
	return dirEntLookup(op, name, payload, elem)
}

// fetch returns the decrypted data associated with the reference.
func (s *Service) fetch(dirRef ref.Reference) ([]byte, error) {
	ciphertext, err := s.Store.Get(dirRef)
	if err != nil {
		return nil, err
	}
	_, payload, err := store.UnpackBlob(ciphertext)
	// TODO check path.
	return payload, nil
}

// Internal representation of directory entries.
// A directory entry is stored as:
//	N length of name, one unsigned byte (255 byte max element name seems fine).
//	N bytes of name.
//	One byte. 0 for regular file, 1 for directory. TODO
//	sha1.Size bytes of Reference.

// dirEntLookup returns the ref for the entry in the named directory whose contents are given in the payload.
// The boolean is true if the entry iteself describes a directory.
func dirEntLookup(op string, pathName path.Name, payload []byte, elem string) (ref.Reference, bool, error) {
	if len(elem) == 0 {
		return r0, false, mkError(op, pathName+"/", "empty name element")
	}
	if len(elem) == 0 || len(elem) > 255 {
		return r0, false, mkError(op, path.Name(elem), "name element too long")
	}
Loop:
	for len(payload) > 0 {
		if len(payload) == 1 {
			return r0, false, mkError(op, pathName, "internal error: invalid directory")
		}
		nameLen := int(payload[0])
		payload = payload[1:]
		if len(payload) < nameLen+1+sha1.Size {
			return r0, false, mkError(op, pathName, "internal error: truncated directory")
		}
		name := payload[:nameLen]
		payload = payload[nameLen:]
		dirByte := payload[0]
		payload = payload[1:]
		hash := payload[:sha1.Size]
		payload = payload[sha1.Size:]
		// Avoid allocation here: don't just convert to string for comparison.
		if nameLen != len(elem) { // Length check is easy and fast.
			continue Loop
		}
		for i, c := range name {
			if c != elem[i] {
				continue Loop
			}
		}
		var r ref.Reference
		copy(r.Hash[:], hash)
		return r, dirByte == 1, nil
	}
	return r0, false, mkError(op, pathName, "no such directory entry: "+elem)
}

// installEntry installs the entry in the directory referenced by dirRef, appending or overwriting the
// entry as required. It returns the ref for the updated directory.
func (s *Service) installEntry(op string, dirName string, dirRef ref.Reference, entry Entry, dirOverwriteOK bool) (ref.Reference, error) {
	dirData, err := s.fetch(dirRef)
	if err != nil {
		fmt.Println("INSTALL fetch FAILED", err)
		return r0, err
	}
	found := false
Loop:
	for payload := dirData; len(payload) > 0 && !found; {
		if len(payload) == 1 {
			return r0, errors.New("invalid directory: no room for name")
		}
		nameLen := int(payload[0])
		payload = payload[1:]
		if len(payload) < nameLen+1+sha1.Size {
			return r0, errors.New("invalid directory: entry truncated")
		}
		name := payload[:nameLen]
		payload = payload[nameLen:]
		isDir := payload[0] != 0
		payload = payload[1:]
		hash := payload[:sha1.Size]
		payload = payload[sha1.Size:]
		// Avoid allocation here: don't just convert to string for comparison.
		if nameLen != len(entry.Elem) { // Length check is easy and fast.
			continue Loop
		}
		for i, c := range name {
			if c != entry.Elem[i] {
				continue Loop
			}
		}
		// We found the reference.
		// If it's already there and is not expected to be a directory, this is an error.
		if isDir && !dirOverwriteOK {
			return r0, mkError(op, path.Name(dirName), "cannot overwrite directory")
		}
		// Overwrite in place.
		copy(hash, entry.ref.Hash[:])
		found = true
	}
	if !found {
		entry := newEntryBytes(entry.Elem, entry.IsDir, entry.ref)
		dirData = append(dirData, entry...)
	}
	blob := store.MakeBlob(string(dirName), dirData)
	dirRef, err = s.Store.Put(blob)
	if err != nil {
		fmt.Println("INSTALL FAILED", err)
		// TODO: System is now inconsistent.
		return r0, err
	}
	return dirRef, err
}
