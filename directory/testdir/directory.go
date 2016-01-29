// Package testdir implements a simple, non-persistent, in-memory directory service.
package testdir

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	goPath "path"
	"sort"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/sim/hash"
	"upspin.googlesource.com/upspin.git/store/teststore" // TODO: for MakeBlob only.
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	r0   upspin.Reference
	loc0 upspin.Location
)

// Service implements directories and file-level I/O.
type Service struct {
	StoreAddr upspin.NetAddr
	Store     upspin.Store
	Root      map[upspin.UserName]upspin.Reference // TODO. No need for hint, they're all on ds.Store.
}

var _ upspin.Directory = (*Service)(nil)

// NewService returns a new, empty directory server that will store its data in the specified store service.
func NewService(ss upspin.Store) *Service {
	return &Service{
		StoreAddr: ss.NetAddr(),
		Store:     ss,
		Root:      make(map[upspin.UserName]upspin.Reference),
	}
}

// entry represents the metadata for a file in a directory.
type entry struct {
	elem  string           // Path element, such as "foo" representing the file a@b.c/a/b/c/foo.
	isDir bool             // The referenced item is itself a directory.
	ref   upspin.Reference // Not hinted, so replicas hold the same data. Directories are near blob servers.
}

// mkStrError creates an os.PathError from the arguments including a string for the error description.
func mkStrError(op string, name upspin.PathName, err string) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(name),
		Err:  errors.New(err),
	}
}

// mkPathError creates an os.PathError from the arguments.
func mkError(op string, name upspin.PathName, err error) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(name),
		Err:  err,
	}
}

// step is an internal function that advances one directory entry given the cleartext
// of the directory's contents.
func (s *Service) step(op string, pathName upspin.PathName, payload []byte) (remaining []byte, name []byte, hashBytes []byte, isDir bool, err error) {
	if len(payload) == 1 {
		err = mkStrError(op, pathName, "internal error: invalid directory")
		return
	}
	nameLen := int(payload[0])
	payload = payload[1:]
	if len(payload) < nameLen+1+sha1.Size {
		err = mkStrError(op, pathName, "internal error: truncated directory")
		return
	}
	name = payload[:nameLen]
	payload = payload[nameLen:]
	isDir = payload[0] != 0
	payload = payload[1:]
	hashBytes = payload[:sha1.Size]
	remaining = payload[sha1.Size:]
	return
}

// Glob matches the pattern against the file names of the full rooted tree.
// That is, the pattern must look like a full path name, but elements of the
// path may contain metacharacters. Matching is done using Go's path.Match
// elementwise. The user name must be present in the pattern and is treated
// as a literal even if it contains metacharacters. The metadata in each entry
// has no key information.
func (s *Service) Glob(pattern string) ([]*upspin.DirEntry, error) {
	// We can use Parse for this because it's only looking for slashes.
	parsed, err := path.Parse(upspin.PathName(pattern + "/"))
	if err != nil {
		return nil, err
	}
	dirRef, ok := s.Root[parsed.User]
	if !ok {
		return nil, mkStrError("Glob", upspin.PathName(parsed.User), "no such user")
	}
	// Loop elementwise along the path, growing the list of candidates breadth-first.
	this := make([]*upspin.DirEntry, 0, 100)
	next := make([]*upspin.DirEntry, 1, 100)
	next[0] = &upspin.DirEntry{
		Name: upspin.PathName(parsed.User),
		Location: upspin.Location{
			NetAddr:   s.StoreAddr,
			Reference: dirRef,
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
			payload, err := s.Fetch(ent.Location.Reference)
			if err != nil {
				return nil, mkStrError("Glob", ent.Name, "internal error: invalid reference")
			}
			for len(payload) > 0 {
				remaining, name, hashBytes, isDir, err := s.step("Get", ent.Name, payload)
				if err != nil {
					return nil, err
				}
				payload = remaining
				matched, err := goPath.Match(elem, string(name))
				if err != nil {
					return nil, mkError("Glob", ent.Name, err)
				}
				if !matched {
					continue
				}
				e := &upspin.DirEntry{
					Name: ent.Name + "/" + upspin.PathName(name),
					Location: upspin.Location{
						NetAddr: s.StoreAddr,
						Reference: upspin.Reference{
							Key:     hash.BytesString(hashBytes),
							Packing: upspin.Debug,
						},
					},
					Metadata: upspin.Metadata{
						IsDir: isDir,
					},
				}
				next = append(next, e)
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

// MakeDirectory creates a new directory with the given name. The user's root must be present.
// TODO: For now at least, only the last entry of the path can be created, as in Unix.
// TODO: We get the reference back. Should we be able to use it instead of a path for Put?
// That Would require more self-checks on directories (easy) and would avoid some name lookup (good)
// but is lower-level. Maybe as an efficiency extra in the API.
func (s *Service) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	parsed, err := path.Parse(directoryName + "/")
	if err != nil {
		return loc0, err
	}
	if len(parsed.Elems) == 0 {
		// Creating a root: easy!
		if _, present := s.Root[parsed.User]; present {
			return loc0, mkStrError("MakeDirectory", directoryName, "already exists")
		}
		blob := teststore.MakeBlob(parsed.String(), nil)
		shaHash := hash.Of(blob)
		ref := upspin.Reference{
			Key:     shaHash.String(),
			Packing: upspin.Debug,
		}
		loc, err := s.Store.Put(ref, blob)
		if err != nil {
			return loc0, err
		}
		s.Root[parsed.User] = loc.Reference
		return loc, nil
	}
	return s.put("MakeDirectory", directoryName, true, nil)
}

// Put creates or overwrites the blob with the specified path.
// The path begins with the user name (which contains no slashes),
// always followed by at least one slash:
//	gopher@google.com/
//	gopher@google.com/a/b/c
// Directories are created with MakeDir. Roots are anyway. TODO.
func (s *Service) Put(pathName upspin.PathName, data []byte) (upspin.Location, error) {
	return s.put("Put", pathName, false, data)
}

// put is the underlying implementation of Put and MakeDirectory.
func (s *Service) put(op string, pathName upspin.PathName, dataIsDir bool, data []byte) (upspin.Location, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return loc0, nil
	}
	if len(parsed.Elems) == 0 {
		return loc0, mkStrError(op, pathName, "cannot create root with Put; use MakeDirectory")
	}
	dirRef, ok := s.Root[parsed.User]
	if !ok {
		// Cannot create user root with Put.
		return loc0, mkStrError(op, upspin.PathName(parsed.User), "no such user")
	}
	// Iterate along the path up to but not past the last element.
	// We remember the entries as we descend for fast(er) overwrite of the Merkle tree.
	// Invariant: dirRef refers to a directory.
	isDir := true
	entries := make([]entry, 0, 10) // 0th entry is the root.
	entries = append(entries, entry{"", true, dirRef})
	for i := 0; i < len(parsed.Elems)-1; i++ {
		elem := parsed.Elems[i]
		dirRef, isDir, err = s.fetchEntry("Put", pathName, dirRef, parsed.Elems[i])
		if err != nil {
			return loc0, err
		}
		if !isDir {
			return loc0, mkStrError(op, parsed.First(i+1).Path(), "not a directory")
		}
		entries = append(entries, entry{elem, true, dirRef}) // TODO: IsDir should be checked
	}
	lastElem := parsed.Elems[len(parsed.Elems)-1]

	// Create a blob storing the data for this file and store it in storage service.
	ciphertext := teststore.MakeBlob(string(pathName), data)
	shaHash := hash.Of(ciphertext)
	ref := upspin.Reference{
		Key:     shaHash.String(),
		Packing: upspin.Debug,
	}
	loc, err := s.Store.Put(ref, ciphertext)
	// TODO VALIDATE REF

	// Update directory holding the file. TODO: must be atomic.
	// Need the name of the directory we're updating.
	ent := entry{
		elem:  lastElem,
		isDir: dataIsDir,
		ref:   loc.Reference,
	}
	dirRef, err = s.installEntry(op, parsed.Drop(1).String(), dirRef, ent, false)
	if err != nil {
		// TODO: System is now inconsistent.
		return loc0, err
	}
	// Rewrite the tree up to the root.
	// Invariant: dirRef identifies the directory that has just been updated.
	// i indicates the directory that needs to be updated to store the new dirRef.
	for i := len(entries) - 2; i >= 0; i-- {
		// Install into the ith directory the (i+1)th entry.
		ent := entry{
			elem:  entries[i+1].elem,
			isDir: true,
			ref:   dirRef,
		}
		dirRef, err = s.installEntry(op, parsed.String(), entries[i].ref, ent, true)
		if err != nil {
			// TODO: System is now inconsistent.
			return loc0, err
		}
	}
	// Update the root.
	s.Root[parsed.User] = dirRef
	href := upspin.Location{
		NetAddr:   s.StoreAddr,
		Reference: loc.Reference,
	}
	return href, nil
}

func (s *Service) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil
	}
	if len(parsed.Elems) == 0 {
		return nil, mkStrError("Lookup", pathName, "cannot use Get on directory; use Glob")
	}
	dirRef, ok := s.Root[parsed.User]
	if !ok {
		return nil, mkStrError("Lookup", upspin.PathName(parsed.User), "no such user")
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(parsed.Elems)-1; i++ {
		dirRef, isDir, err = s.fetchEntry("Lookup", pathName, dirRef, parsed.Elems[i])
		if err != nil {
			return nil, err
		}
		if !isDir {
			return nil, mkStrError("Lookup", pathName, "not a directory")
		}
	}
	lastElem := parsed.Elems[len(parsed.Elems)-1]
	// Destination must exist. If so we need to update the parent directory record.
	var r upspin.Reference
	if r, isDir, err = s.fetchEntry("Lookup", pathName, dirRef, lastElem); err != nil {
		return nil, err
	}
	entry := &upspin.DirEntry{
		Name: parsed.Path(),
		Location: upspin.Location{
			NetAddr: s.StoreAddr,
			Reference: upspin.Reference{
				Key:     r.Key,
				Packing: upspin.Debug,
			},
		},
		Metadata: upspin.Metadata{
			IsDir: isDir,
			// TODO: REST OF METADATA
		},
	}
	return entry, nil
}

func newEntryBytes(elem string, isDir bool, ref upspin.Reference) []byte {
	entry := make([]byte, 0, 1+len(elem)+1+sha1.Size)
	entry = append(entry, byte(len(elem)))
	entry = append(entry, elem...)
	dirByte := byte(0)
	if isDir {
		dirByte = 1
	}
	entry = append(entry, dirByte)
	key, err := hash.Parse(ref.Key)
	if err != nil {
		panic(err)
	}
	entry = append(entry, key[:]...)
	return entry
}

// fetchEntry returns the reference for the named elem within the named directory referenced by dirRef.
// It reads the whole directory, so avoid calling it repeatedly.
func (s *Service) fetchEntry(op string, name upspin.PathName, dirRef upspin.Reference, elem string) (upspin.Reference, bool, error) {
	payload, err := s.Fetch(dirRef)
	if err != nil {
		return r0, false, err
	}
	return s.dirEntLookup(op, name, payload, elem)
}

// Fetch returns the decrypted data associated with the reference.
// TODO: For test but is it genuinely valuable?
func (s *Service) Fetch(dirRef upspin.Reference) ([]byte, error) {
	loc := upspin.Location{
		NetAddr:   s.StoreAddr,
		Reference: dirRef,
	}
	ciphertext, _, err := s.Store.Get(loc)
	if err != nil {
		return nil, err
	}
	_, payload, err := teststore.UnpackBlob(ciphertext)
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
func (s *Service) dirEntLookup(op string, pathName upspin.PathName, payload []byte, elem string) (upspin.Reference, bool, error) {
	if len(elem) == 0 {
		return r0, false, mkStrError(op, pathName+"/", "empty name element")
	}
	if len(elem) > 255 {
		return r0, false, mkStrError(op, upspin.PathName(elem), "name element too long")
	}
Loop:
	for len(payload) > 0 {
		remaining, name, hashBytes, isDir, err := s.step(op, pathName, payload)
		if err != nil {
			return r0, false, err
		}
		payload = remaining
		// Avoid allocation here: don't just convert to string for comparison.
		if len(name) != len(elem) { // Length check is easy and fast.
			continue Loop
		}
		for i, c := range name {
			if c != elem[i] {
				continue Loop
			}
		}
		r := upspin.Reference{
			Key:     hash.BytesString(hashBytes),
			Packing: upspin.Debug,
		}
		return r, isDir, nil
	}
	return r0, false, mkStrError(op, pathName, "no such directory entry: "+elem)
}

// installEntry installs the entry in the directory referenced by dirRef, appending or overwriting the
// entry as required. It returns the ref for the updated directory.
func (s *Service) installEntry(op string, dirName string, dirRef upspin.Reference, ent entry, dirOverwriteOK bool) (upspin.Reference, error) {
	dirData, err := s.Fetch(dirRef)
	if err != nil {
		fmt.Println("INSTALL fetch FAILED", err)
		return r0, err
	}
	found := false
	// fmt.Printf("\nBEFORE: %s\n%q\n\n", dirName, dirData)
Loop:
	for payload := dirData; len(payload) > 0 && !found; {
		remaining, name, hashBytes, isDir, err := s.step(op, upspin.PathName(dirName), payload)
		if err != nil {
			return r0, err
		}
		payload = remaining
		// Avoid allocation here: don't just convert to string for comparison.
		if len(name) != len(ent.elem) { // Length check is easy and fast.
			continue Loop
		}
		for i, c := range name {
			if c != ent.elem[i] {
				continue Loop
			}
		}
		// We found the reference.
		// If it's already there and is not expected to be a directory, this is an error.
		if isDir && !dirOverwriteOK {
			return r0, mkStrError(op, upspin.PathName(dirName), "cannot overwrite directory")
		}
		// Overwrite in place.
		h, err := hash.Parse(ent.ref.Key)
		if err != nil {
			return r0, err
		}
		copy(hashBytes, h[:])
		found = true
	}
	if !found {
		entry := newEntryBytes(ent.elem, ent.isDir, ent.ref)
		dirData = append(dirData, entry...)
	}
	// fmt.Printf("\n%s\n%q\n\n", dirName, dirData)
	blob := teststore.MakeBlob(string(dirName), dirData)
	shaHash := hash.Of(blob)
	ref := upspin.Reference{
		Key:     shaHash.String(),
		Packing: upspin.Debug,
	}
	loc, err := s.Store.Put(ref, blob)
	if err != nil {
		fmt.Println("INSTALL FAILED", err)
		// TODO: System is now inconsistent.
		return r0, err
	}
	return loc.Reference, err
}

// Methods to implement upspin.Access

func (s *Service) ServerUserName() string {
	return "testuser"
}

func (s *Service) Dial(context upspin.ClientContext, loc upspin.Location) (interface{}, error) {
	sloc := loc
	sloc.AccessName = "teststore" // TODO HACK.
	// TODO: This package only works if the same Store instance is used for data and
	// to store its own DirEntries. It should be improved, but at the moment the only
	// addresses it knows about come from its Store.
	store, err := access.Switch.BindStore(context, sloc)
	if err != nil {
		return nil, err
	}
	return NewService(store), nil
}

func init() {
	service := &Service{
		Root: make(map[upspin.UserName]upspin.Reference),
	}
	access.Switch.RegisterDirectory("testdir", service)
}
