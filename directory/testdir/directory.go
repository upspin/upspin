// Package testdir implements a simple, non-persistent, in-memory directory service.
package testdir

import (
	"errors"
	"fmt"
	"os"
	goPath "path"
	"sort"
	"sync"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"

	// Imported because it's used to pack dir entries.
	_ "upspin.googlesource.com/upspin.git/pack/plain"
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
	endpoint      upspin.Endpoint
	StoreEndpoint upspin.Endpoint
	Store         upspin.Store
	Context       *upspin.Context

	// mu is used to serialize access to the Root map.
	// It's also used to serialize all access to the store through the
	// exported API, for simple but slow safety. At least it's an RWMutex
	// so it's not _too_ bad.
	mu   sync.RWMutex
	Root map[upspin.UserName]*upspin.DirEntry
}

var _ upspin.Directory = (*Service)(nil)

// mkStrError creates an os.PathError from the arguments including a string for the error description.
func mkStrError(op string, name upspin.PathName, err string) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(name),
		Err:  errors.New(err),
	}
}

// mkError creates an os.PathError from the arguments.
func mkError(op string, name upspin.PathName, err error) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(name),
		Err:  err,
	}
}

func newDirEntry(context *upspin.Context, name upspin.PathName) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name: name,
		Location: upspin.Location{
			Endpoint: context.Store.Endpoint(),
		},
		Metadata: upspin.Metadata{
			PackData: upspin.PackData{byte(dirPacking)},
		},
	}
}

func packDirBlob(context *upspin.Context, cleartext []byte, name upspin.PathName) ([]byte, *upspin.Metadata, error) {
	return packBlob(context, cleartext, newDirEntry(context, name))
}

func getPacker(packdata upspin.PackData) (upspin.Packer, error) {
	if len(packdata) == 0 {
		return nil, errors.New("no packdata")
	}
	packer := pack.Lookup(upspin.Packing(packdata[0]))
	if packer == nil {
		return nil, fmt.Errorf("no packing %#x registered", packdata[0])
	}
	return packer, nil
}

// packBlob packs an arbitrary blob and its metadata.
func packBlob(context *upspin.Context, cleartext []byte, entry *upspin.DirEntry) ([]byte, *upspin.Metadata, error) {
	packer, err := getPacker(entry.Metadata.PackData)
	if err != nil {
		return nil, nil, err
	}
	cipherLen := packer.PackLen(context, cleartext, entry)
	if cipherLen < 0 {
		return nil, nil, errors.New("PackLen failed")
	}
	ciphertext := make([]byte, cipherLen)
	n, err := packer.Pack(context, ciphertext, cleartext, entry)
	if err != nil {
		return nil, nil, err
	}
	return ciphertext[:n], &entry.Metadata, nil
}

// unpackBlob unpacks a blob.
// Other than from unpackDirBlob, only used in tests.
func unpackBlob(context *upspin.Context, ciphertext []byte, entry *upspin.DirEntry) ([]byte, error) {
	packer, err := getPacker(entry.Metadata.PackData)
	if err != nil {
		return nil, err
	}
	clearLen := packer.UnpackLen(context, ciphertext, entry)
	if clearLen < 0 {
		return nil, errors.New("UnpackLen failed")
	}
	cleartext := make([]byte, clearLen)
	n, err := packer.Unpack(context, cleartext, ciphertext, entry)
	if err != nil {
		return nil, err
	}
	return cleartext[:n], nil
}

// unpackDirBlob unpacks a blob that is known to be a directory record.
func unpackDirBlob(context *upspin.Context, ciphertext []byte, name upspin.PathName) ([]byte, error) {
	return unpackBlob(context, ciphertext, newDirEntry(context, name))
}

// Glob matches the pattern against the file names of the full rooted tree.
// That is, the pattern must look like a full path name, but elements of the
// path may contain metacharacters. Matching is done using Go's path.Match
// elementwise. The user name must be present in the pattern and is treated
// as a literal even if it contains metacharacters. The metadata in each entry
// has no reference information.
func (s *Service) Glob(pattern string) ([]*upspin.DirEntry, error) {
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	dirEntry, ok := s.Root[parsed.User]
	if !ok {
		return nil, mkStrError("Glob", upspin.PathName(parsed.User), "no such user")
	}
	dirRef := dirEntry.Location.Reference
	// Loop elementwise along the path, growing the list of candidates breadth-first.
	this := make([]*upspin.DirEntry, 0, 100)
	next := make([]*upspin.DirEntry, 1, 100)
	next[0] = &upspin.DirEntry{
		Name: parsed.First(0).Path(), // The root.
		Location: upspin.Location{
			Reference: dirRef,
			Endpoint:  s.StoreEndpoint,
		},
		Metadata: upspin.Metadata{
			IsDir: true,
		},
	}
	for _, elem := range parsed.Elems {
		this, next = next, this[:0]
		for _, ent := range this {
			// ent must refer to a directory.
			if !ent.Metadata.IsDir {
				continue
			}
			payload, err := s.fetchDir(ent.Location.Reference, ent.Name)
			if err != nil {
				return nil, mkStrError("Glob", ent.Name, "internal error: invalid reference: "+err.Error())
			}
			for len(payload) > 0 {
				var nextEntry upspin.DirEntry
				remaining, err := nextEntry.Unmarshal(payload)
				if err != nil {
					return nil, err
				}
				payload = remaining
				parsed, err := path.Parse(nextEntry.Name)
				if err != nil {
					return nil, err
				}
				matched, err := goPath.Match(elem, parsed.Elems[len(parsed.Elems)-1])
				if err != nil {
					return nil, mkError("Glob", ent.Name, err)
				}
				if !matched {
					continue
				}
				next = append(next, &nextEntry)
			}
		}
	}
	// Need a / on the root if it's matched.
	for _, e := range next {
		if e.Name == upspin.PathName(parsed.User) {
			e.Name += "/"
		}
	}
	sort.Sort(dirEntrySlice(next))
	return next, err
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
			Endpoint:  s.StoreEndpoint,
			Reference: ref,
		},
		Metadata: upspin.Metadata{
			IsDir:    true,
			Sequence: seq,
			Size:     0,
			Time:     upspin.Now(),
			PackData: upspin.PackData{byte(dirPacking)},
		},
	}
}

// MakeDirectory creates a new directory with the given name. The user's root must be present.
// TODO: For now at least, only the last entry of the path can be created, as in Unix.
func (s *Service) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	parsed, err := path.Parse(directoryName)
	if err != nil {
		return loc0, err
	}
	pathName := parsed.Path()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parsed.Elems) == 0 {
		// Creating a root: easy!
		if _, present := s.Root[parsed.User]; present {
			return loc0, mkStrError("MakeDirectory", directoryName, "already exists")
		}
		blob, _, err := packDirBlob(s.Context, nil, pathName) // TODO: Ignoring metadata (but using PlainPack).
		if err != nil {
			return loc0, err
		}
		ref, err := s.Store.Put(blob)
		if err != nil {
			return loc0, err
		}
		dirEntry := s.rootDirEntry(parsed.User, ref, 0)
		s.Root[parsed.User] = dirEntry
		return dirEntry.Location, nil
	}
	// Use parsed.Path() rather than directoryName so it's canonicalized.
	ref, err := s.Store.Put([]byte{}) // Nothing to store, but we need a reference.
	if err != nil {
		return loc0, err
	}
	loc := upspin.Location{
		Endpoint:  s.Store.Endpoint(),
		Reference: ref,
	}
	entry := newDirEntry(s.Context, parsed.Path())
	entry.Metadata.IsDir = true
	entry.Location = loc
	return loc, s.put("MakeDirectory", true, loc, entry)
}

// Put creates or overwrites the blob with the specified path.
// The path begins with the user name (which contains no slashes),
// always followed by at least one slash:
//	gopher@google.com/
//	gopher@google.com/a/b/c
// Directories are created with MakeDirectory. Roots are anyway. TODO.
func (s *Service) Put(loc upspin.Location, entry *upspin.DirEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Use parsed.Path() rather than directoryName so it's canonicalized.
	return s.put("Put", false, loc, entry)
}

// put is the underlying implementation of Put and MakeDirectory.
func (s *Service) put(op string, dataIsDir bool, loc upspin.Location, entry *upspin.DirEntry) error {
	parsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil
	}
	pathName := parsed.Path()
	if len(parsed.Elems) == 0 {
		return mkStrError(op, pathName, "cannot create root with Put; use MakeDirectory")
	}
	dirEntry, ok := s.Root[parsed.User]
	if !ok {
		// Cannot create user root with Put.
		return mkStrError(op, upspin.PathName(parsed.User), "no such user")
	}
	dirRef := dirEntry.Location.Reference
	// Iterate along the path up to but not past the last element.
	// We remember the entries as we descend for fast(er) overwrite of the Merkle tree.
	// Invariant: dirRef refers to a directory.
	entries := make([]*upspin.DirEntry, 0, 10) // 0th entry is the root.
	entries = append(entries, dirEntry)
	for i := 0; i < len(parsed.Elems)-1; i++ {
		e, err := s.fetchEntry("Put", parsed.First(i).Path(), dirRef, parsed.Elems[i])
		if err != nil {
			return err
		}
		if !e.Metadata.IsDir {
			return mkStrError(op, parsed.First(i+1).Path(), "not a directory")
		}
		entries = append(entries, e)
		dirRef = e.Location.Reference
	}
	dirRef, err = s.installEntry(op, parsed.Drop(1).Path(), dirRef, entry, false)
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
				Endpoint:  s.StoreEndpoint,
				Reference: dirRef,
			},
			Metadata: upspin.Metadata{
				IsDir:    true,
				Sequence: entries[i+1].Metadata.Sequence,
				PackData: upspin.PackData{byte(dirPacking)},
			},
		}
		dirRef, err = s.installEntry(op, parsed.First(i).Path(), entries[i].Location.Reference, dirEntry, true)
		if err != nil {
			// TODO: System is now inconsistent.
			return err
		}
	}
	// Update the root.
	seq := s.Root[parsed.User].Metadata.Sequence
	s.Root[parsed.User] = s.rootDirEntry(parsed.User, dirRef, seq+1)

	return nil
}

// Lookup returns the directory entry for the named file.
func (s *Service) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	dirEntry, ok := s.Root[parsed.User]
	if !ok {
		return nil, mkStrError("Lookup", upspin.PathName(parsed.User), "no such user")
	}
	if parsed.IsRoot() {
		return dirEntry, nil
	}
	dirRef := dirEntry.Location.Reference
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	for i := 0; i < len(parsed.Elems)-1; i++ {
		entry, err := s.fetchEntry("Lookup", parsed.First(i).Path(), dirRef, parsed.Elems[i])
		if err != nil {
			return nil, err
		}
		if !entry.Metadata.IsDir {
			return nil, mkStrError("Lookup", pathName, "not a directory")
		}
		dirRef = entry.Location.Reference
	}
	lastElem := parsed.Elems[len(parsed.Elems)-1]
	// Destination must exist. If so we need to update the parent directory record.
	entry, err := s.fetchEntry("Lookup", parsed.Drop(1).Path(), dirRef, lastElem)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// fetchEntry returns the reference for the named elem within the named directory referenced by dirRef.
// It reads the whole directory, so avoid calling it repeatedly.
func (s *Service) fetchEntry(op string, name upspin.PathName, dirRef upspin.Reference, elem string) (*upspin.DirEntry, error) {
	payload, err := s.fetchDir(dirRef, name)
	if err != nil {
		return nil, err
	}
	return s.dirEntLookup(op, name, payload, elem)
}

// fetchDir returns the decrypted directory data associated with the reference.
func (s *Service) fetchDir(dirRef upspin.Reference, name upspin.PathName) ([]byte, error) {
	ciphertext, locs, err := s.Store.Get(dirRef)
	if err != nil {
		return nil, err
	}
	if locs != nil {
		ciphertext, _, err = s.Store.Get(locs[0].Reference)
		if err != nil {
			return nil, err
		}
	}
	payload, err := unpackDirBlob(s.Context, ciphertext, name)
	return payload, err
}

// dirEntLookup returns the ref for the entry in the named directory whose contents are given in the payload.
// The boolean is true if the entry itself describes a directory.
func (s *Service) dirEntLookup(op string, pathName upspin.PathName, payload []byte, elem string) (*upspin.DirEntry, error) {
	if len(elem) == 0 {
		return nil, mkStrError(op, pathName+"/", "empty name element")
	}
	fileName := path.Join(pathName, elem)
	var entry upspin.DirEntry
Loop:
	for len(payload) > 0 {
		remaining, err := entry.Unmarshal(payload)
		if err != nil {
			return nil, err
		}
		payload = remaining
		if fileName != entry.Name {
			continue Loop
		}
		return &entry, nil
	}
	return nil, mkStrError(op, pathName, "no such directory entry: "+elem)
}

var errSeq = errors.New("sequence mismatch")

// installEntry installs the new entry in the directory referenced by dirLeu, appending or overwriting the
// entry as required. It returns the ref for the updated directory.
func (s *Service) installEntry(op string, dirName upspin.PathName, dirRef upspin.Reference, newEntry *upspin.DirEntry, dirOverwriteOK bool) (upspin.Reference, error) {
	if dirRef == "" {
		panic("nothing")
	}
	dirData, err := s.fetchDir(dirRef, dirName)
	if err != nil {
		return "", err
	}
	found := false
	var nextEntry upspin.DirEntry
	for payload := dirData; len(payload) > 0 && !found; {
		// Remember where this entry starts.
		start := len(dirData) - len(payload)
		remaining, err := nextEntry.Unmarshal(payload)
		if err != nil {
			return "", err
		}
		length := len(payload) - len(remaining)
		payload = remaining
		if nextEntry.Name != newEntry.Name {
			continue
		}
		// We found the item.
		// If it's already there and is not expected to be a directory, this is an error.
		if nextEntry.Metadata.IsDir && !dirOverwriteOK {
			return "", mkStrError(op, upspin.PathName(dirName), "cannot overwrite directory")
		}
		// Drop this entry so we can append the updated one.
		// It may have changed length because of the metadata being unpredictable,
		// so we cannot overwrite it in place.
		copy(dirData[start:], remaining)
		dirData = dirData[:len(dirData)-length]
		// We want nextEntry's sequence (previous value+1) but everything else from newEntry.
		if newEntry.Metadata.Sequence != 0 {
			if newEntry.Metadata.Sequence != nextEntry.Metadata.Sequence {
				return "", mkError(op, newEntry.Name, errSeq)
			}
		}
		newEntry.Metadata.Sequence = nextEntry.Metadata.Sequence + 1
		break
	}
	data, err := newEntry.Marshal()
	if err != nil {
		return "", err
	}
	dirData = append(dirData, data...)
	blob, _, err := packDirBlob(s.Context, dirData, dirName) // TODO: Ignoring metadata (but using PlainPack).
	ref, err := s.Store.Put(blob)
	if err != nil {
		// TODO: System is now inconsistent.
		return "", err
	}
	return ref, nil
}

// Methods to implement upspin.Dialer

// ServerUserName implements upspin.Dialer.
func (s *Service) ServerUserName() string {
	return "" // No one is authenticated.
}

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (s *Service) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("testdir: unrecognized transport")
	}

	s.Store = context.Store
	s.StoreEndpoint = context.Store.Endpoint()
	s.endpoint = e
	s.Context = context
	return s, nil
}

// Endpoint implements upspin.Directory.Endpoint.
func (s *Service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

const transport = upspin.InProcess

func init() {
	s := &Service{
		endpoint: upspin.Endpoint{}, // uninitialized until Dial time.
		Store:    nil,               // uninitialized until Dial time.
		Root:     make(map[upspin.UserName]*upspin.DirEntry),
	}
	bind.RegisterDirectory(transport, s)
}
