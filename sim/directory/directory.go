package directory

import (
	"crypto/sha1"
	"errors"
	"fmt"

	"upspin.googlesource.com/upspin.git/sim/path"
	"upspin.googlesource.com/upspin.git/sim/ref"
	"upspin.googlesource.com/upspin.git/sim/store"
)

var (
	r0  ref.Reference
	hr0 ref.HintedReference
)

// Service implements directories. The actual storage may be on the
// same machine or a different one.
type Service struct {
	StoreLocation ref.Location
	Store         *store.Service
	Root          map[path.UserName]ref.Reference // TODO. No need for hint, they're all on ds.Store.
}

func NewService(ss *store.Service) *Service {
	return &Service{
		StoreLocation: ss.Location,
		Store:         ss,
		Root:          make(map[path.UserName]ref.Reference),
	}
}

type DirEntry struct {
	Name          string
	ref.Reference // Not hinted, so replicas hold the same data. Directories are near blob servers.
}

func (s *Service) Lookup(pathName path.Name) (ref.HintedReference, error) {
	return hr0, nil
}

func (s *Service) Glob(pattern string) ([]DirEntry, error) {
	return nil, nil
}

// TODO: Get the reference back. Should we be able to use it instead of a path for Put?
// Would require more self-checks on directories (easy) and would avoid some name lookup (good)
// but is lower-level. Maybe as an efficiency extra in the API.
func (s *Service) MakeDirectory(directoryName path.Name) (ref.HintedReference, error) {
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	path, err := path.Parse(directoryName + "/")
	if err != nil {
		return hr0, nil
	}
	if len(path.Elems) == 0 {
		// Easy!
		if _, present := s.Root[path.User]; present {
			return hr0, fmt.Errorf("directory %q already exists", directoryName)
		}
		blob := store.MakeBlob(path.String(), nil)
		r, err := s.Store.Put(blob)
		if err != nil {
			return hr0, nil
		}
		href := ref.HintedReference{
			Reference: ref.Reference{r.Hash},
			Location:  s.StoreLocation,
		}
		s.Root[path.User] = href.Reference
		return href, nil
	}
	return hr0, fmt.Errorf("TODO: can only make root")
}

// Put creates or overwrites the blob with the specified path.
// The path begins with the user name (which contains no slashes),
// always followed by at least one slash:
//	gopher@google.com/
//	gopher@google.com/a/b/c
// Directories are created with MakeDir. Roots are anyway. TODO.
func (s *Service) Put(pathName path.Name, data []byte) (ref.HintedReference, error) {
	path, err := path.Parse(pathName)
	if err != nil {
		return hr0, nil
	}
	if len(path.Elems) == 0 {
		return hr0, fmt.Errorf("cannot create root %q with Put", pathName)
	}
	dirRef, ok := s.Root[path.User]
	if !ok {
		// TODO: Make an error type.
		return hr0, fmt.Errorf("no user %q", path.User) // NOTE: Cannot create user root.
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(path.Elems)-1; i++ {
		fmt.Printf("IN LOOP - SHOULD NOT HAPPEN")
		dirRef, isDir, err = s.fetchEntry(dirRef, path.Elems[i])
		if err != nil {
			return hr0, nil
		}
		if !isDir {
			return hr0, fmt.Errorf("not a directory: %q", pathName)
		}
	}
	lastElem := path.Elems[len(path.Elems)-1]
	// Destination might exist. If so we need to update the parent directory record.
	// TODO: we just fail now.
	if _, _, err := s.fetchEntry(dirRef, lastElem); err == nil {
		return hr0, errors.New("overwriting unimplemented")
	}
	ciphertext := store.MakeBlob(string(pathName), data)
	r, err := s.Store.Put(ciphertext)
	// TODO VALIDATE REF
	// Update directory. TODO: must be atomic.
	dirData, err := s.fetch(dirRef)
	if err != nil {
		return hr0, err
	}
	entry := newEntry(lastElem, false, r)
	dirData = append(dirData, entry...) // TODO: Cannot append if this is an update.
	// Need the name of the directory we're updating.
	dirPath := path
	dirPath.Elems = dirPath.Elems[:len(dirPath.Elems)-1]
	blob := store.MakeBlob(dirPath.String(), dirData)
	dirRef, err = s.Store.Put(blob)
	if err != nil {
		// TODO: System is now inconsistent.
		return hr0, err
	}
	// For now we can only put files in the root.
	if len(path.Elems) != 1 {
		panic("TODO: BUBBLE DIRREF TO ROOT")
	}
	if len(path.Elems) == 1 { // Update root.
		s.Root[path.User] = dirRef
	}
	href := ref.HintedReference{
		Reference: r,
		Location:  s.StoreLocation,
	}
	return href, nil
}

func (s *Service) Get(pathName path.Name) (ref.HintedReference, []byte, error) {
	path, err := path.Parse(pathName)
	if err != nil {
		return hr0, nil, nil
	}
	if len(path.Elems) == 0 {
		return hr0, nil, fmt.Errorf("cannot Get directory %q", pathName)
	}
	dirRef, ok := s.Root[path.User]
	if !ok {
		// TODO: Make an error type.
		return hr0, nil, fmt.Errorf("no user %q", path.User)
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(path.Elems)-1; i++ {
		dirRef, isDir, err = s.fetchEntry(dirRef, path.Elems[i])
		if err != nil {
			return hr0, nil, nil
		}
		if !isDir {
			return hr0, nil, fmt.Errorf("not a directory: %q", pathName)
		}
	}
	lastElem := path.Elems[len(path.Elems)-1]
	// Destination must exist. If so we need to update the parent directory record.
	var r ref.Reference
	if r, isDir, err = s.fetchEntry(dirRef, lastElem); err != nil {
		return hr0, nil, err
	}
	if isDir {
		return hr0, nil, fmt.Errorf("is a directory: %q", pathName)
	}
	ciphertext, err := s.Store.Get(r)
	if err != nil {
		return hr0, nil, fmt.Errorf("get blob: %v", err)
	}
	name, cleartext, err := store.UnpackBlob(ciphertext)
	if err != nil {
		return hr0, nil, fmt.Errorf("unpack blob: %v", err)
	}
	// TODO: Check name.
	_ = name
	href := ref.HintedReference{
		Reference: r,
		Location:  s.StoreLocation,
	}
	return href, cleartext, nil
}

func newEntry(elem string, isDir bool, ref ref.Reference) []byte {
	entry := make([]byte, 0, 1+len(elem)+1+sha1.Size)
	entry = append(entry, byte(len(elem)))
	entry = append(entry, elem...)
	entry = append(entry, 0) // Not a directory
	entry = append(entry, ref.Hash[:]...)
	return entry
}

func (s *Service) fetchEntry(dirRef ref.Reference, elem string) (ref.Reference, bool, error) {
	payload, err := s.fetch(dirRef)
	if err != nil {
		return r0, false, err
	}
	return dirEntLookup(payload, elem)
}

func (s *Service) fetch(dirRef ref.Reference) ([]byte, error) {
	ciphertext, err := s.Store.Get(dirRef)
	if err != nil {
		fmt.Println("fetch failed", err)
		return nil, err
	}
	_, payload, err := store.UnpackBlob(ciphertext)
	// TODO check path.
	return payload, nil
}

// Directory entries.
// A directory entry is stored as:
//	N length of name, one unsigned byte (255 byte max element name seems fine).
//	N bytes of name.
//	One byte. 0 for regular file, 1 for directory. TODO
//	sha1.Size bytes of Reference.

// The boolean is true if the entry is for a directory.
func dirEntLookup(payload []byte, elem string) (ref.Reference, bool, error) {
Loop:
	for len(payload) > 0 {
		if len(payload) == 1 {
			return r0, false, errors.New("invalid directory: no room for name")
		}
		nameLen := int(payload[0])
		payload = payload[1:]
		if len(payload) < nameLen+1+sha1.Size {
			return r0, false, errors.New("invalid directory: entry truncated")
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
	return r0, false, fmt.Errorf("no such directory entry %q", elem) // TODO build a better error type.
}
