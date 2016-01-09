package service

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	// We use the word path a lot.
)

// Types for API elements to make descriptions easier to understand.
type Reference struct {
	Hash
}
type UserName string
type PathName string

// HintedReference attaches a location hint to a Reference.
type HintedReference struct {
	Reference
	Location // TODO: Should be a list of locations, but makes the map harder in DirectoryService (sigh).
}

func (hr HintedReference) String() string {
	return fmt.Sprintf("%s!%s", hr.Location, hr.Reference)
}

var (
	r0  Reference
	hr0 HintedReference
)

// Location represents a service. Maybe just a net.Addr, maybe more.
type Location struct {
	Addr net.Addr
}

// The various XXXService types approximate a network server and its interface.

// UserService maps user names to references to the root of the user's tree.
type UserService struct {
	root map[UserName][]Location
}

// UserLookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are fallbacks.
func (us *UserService) UserLookup(name UserName) ([]Location, error) {
	locs, ok := us.root[name]
	if !ok {
		return nil, errors.New("no such user")
	}
	return locs, nil
}

// DirectoryService implements directories. The actual storage may be on the
// same machine or a different one.
type DirectoryService struct {
	StoreLocation Location
	Store         *StorageService
	Root          map[UserName]Reference // TODO. No need for hint, they're all on ds.Store.
}

func NewDirectoryService(ss *StorageService) *DirectoryService {
	return &DirectoryService{
		StoreLocation: ss.Location,
		Store:         ss,
		Root:          make(map[UserName]Reference),
	}
}

type DirEntry struct {
	Name      string
	Reference // Not hinted, so replicas hold the same data. Directories are near blob servers.
}

func (ds *DirectoryService) Lookup(path PathName) (HintedReference, error) {
	return hr0, nil
}

func (ds *DirectoryService) Glob(pattern string) ([]DirEntry, error) {
	return nil, nil
}

// TODO: Get the reference back. Should we be able to use it instead of a path for Put?
// Would require more self-checks on directories (easy) and would avoid some name lookup (good)
// but is lower-level. Maybe as an efficiency extra in the API.
func (ds *DirectoryService) MakeDirectory(directoryName PathName) (HintedReference, error) {
	// The name must end in / so parse will work, but adding one if it's already there
	// is fine - the path is cleaned.
	path, err := parse(directoryName + "/")
	if err != nil {
		return hr0, nil
	}
	if len(path.elems) == 0 {
		// Easy!
		if _, present := ds.Root[path.user]; present {
			return hr0, fmt.Errorf("directory %q already exists", directoryName)
		}
		blob := makeBlob(path.String(), nil)
		ref, err := ds.Store.Put(blob)
		if err != nil {
			return hr0, nil
		}
		href := HintedReference{
			Reference: Reference{ref.Hash},
			Location:  ds.StoreLocation,
		}
		ds.Root[path.user] = href.Reference
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
func (ds *DirectoryService) Put(pathName PathName, data []byte) (HintedReference, error) {
	path, err := parse(pathName)
	if err != nil {
		return hr0, nil
	}
	if len(path.elems) == 0 {
		return hr0, fmt.Errorf("cannot create root %q with Put", pathName)
	}
	dirRef, ok := ds.Root[path.user]
	if !ok {
		// TODO: Make an error type.
		return hr0, fmt.Errorf("no user %q", path.user) // NOTE: Cannot create user root.
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(path.elems)-1; i++ {
		fmt.Printf("IN LOOP - SHOULD NOT HAPPEN")
		dirRef, isDir, err = ds.fetchEntry(dirRef, path.elems[i])
		if err != nil {
			return hr0, nil
		}
		if !isDir {
			return hr0, fmt.Errorf("not a directory: %q", pathName)
		}
	}
	lastElem := path.elems[len(path.elems)-1]
	// Destination might exist. If so we need to update the parent directory record.
	// TODO: we just fail now.
	if _, _, err := ds.fetchEntry(dirRef, lastElem); err == nil {
		return hr0, errors.New("overwriting unimplemented")
	}
	ciphertext := makeBlob(string(pathName), data)
	ref, err := ds.Store.Put(ciphertext)
	// TODO VALIDATE REF
	// Update directory. TODO: must be atomic.
	dirData, err := ds.fetch(dirRef)
	if err != nil {
		return hr0, err
	}
	entry := newEntry(lastElem, false, ref)
	dirData = append(dirData, entry...) // TODO: Cannot append if this is an update.
	// Need the name of the directory we're updating.
	dirPath := path
	dirPath.elems = dirPath.elems[:len(dirPath.elems)-1]
	blob := makeBlob(dirPath.String(), dirData)
	dirRef, err = ds.Store.Put(blob)
	if err != nil {
		// TODO: System is now inconsistent.
		return hr0, err
	}
	// For now we can only put files in the root.
	if len(path.elems) != 1 {
		panic("TODO: BUBBLE DIRREF TO ROOT")
	}
	if len(path.elems) == 1 { // Update root.
		ds.Root[path.user] = dirRef
	}
	href := HintedReference{
		Reference: ref,
		Location:  ds.StoreLocation,
	}
	return href, nil
}

func (ds *DirectoryService) Get(pathName PathName) (HintedReference, []byte, error) {
	path, err := parse(pathName)
	if err != nil {
		return hr0, nil, nil
	}
	if len(path.elems) == 0 {
		return hr0, nil, fmt.Errorf("cannot Get directory %q", pathName)
	}
	dirRef, ok := ds.Root[path.user]
	if !ok {
		// TODO: Make an error type.
		return hr0, nil, fmt.Errorf("no user %q", path.user)
	}
	// Iterate along the path up to but not past the last element.
	// Invariant: dirRef refers to a directory.
	isDir := true
	for i := 0; i < len(path.elems)-1; i++ {
		dirRef, isDir, err = ds.fetchEntry(dirRef, path.elems[i])
		if err != nil {
			return hr0, nil, nil
		}
		if !isDir {
			return hr0, nil, fmt.Errorf("not a directory: %q", pathName)
		}
	}
	lastElem := path.elems[len(path.elems)-1]
	// Destination must exist. If so we need to update the parent directory record.
	var ref Reference
	if ref, isDir, err = ds.fetchEntry(dirRef, lastElem); err != nil {
		return hr0, nil, err
	}
	if isDir {
		return hr0, nil, fmt.Errorf("is a directory: %q", pathName)
	}
	ciphertext, err := ds.Store.Get(ref)
	if err != nil {
		return hr0, nil, fmt.Errorf("get blob: %v", err)
	}
	name, cleartext, err := unpackBlob(ciphertext)
	if err != nil {
		return hr0, nil, fmt.Errorf("unpack blob: %v", err)
	}
	// TODO: Check name.
	_ = name
	href := HintedReference{
		Reference: ref,
		Location:  ds.StoreLocation,
	}
	return href, cleartext, nil
}

func newEntry(elem string, isDir bool, ref Reference) []byte {
	entry := make([]byte, 0, 1+len(elem)+1+sha1.Size)
	entry = append(entry, byte(len(elem)))
	entry = append(entry, elem...)
	entry = append(entry, 0) // Not a directory
	entry = append(entry, ref.Hash[:]...)
	return entry
}

func (ds *DirectoryService) fetchEntry(dirRef Reference, elem string) (Reference, bool, error) {
	payload, err := ds.fetch(dirRef)
	if err != nil {
		return r0, false, err
	}
	return dirEntLookup(payload, elem)
}

func (ds *DirectoryService) fetch(dirRef Reference) ([]byte, error) {
	ciphertext, err := ds.Store.Get(dirRef)
	if err != nil {
		fmt.Println("fetch failed", err)
		return nil, err
	}
	_, payload, err := unpackBlob(ciphertext)
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
func dirEntLookup(payload []byte, elem string) (Reference, bool, error) {
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
		var r Reference
		copy(r.Hash[:], hash)
		return r, dirByte == 1, nil
	}
	return r0, false, fmt.Errorf("no such directory entry %q", elem) // TODO build a better error type.
}

// Blobs.
// Message is {N, path[N], data}. N is unsigned varint-encoded.

func makeBlob(path string, payload []byte) []byte {
	if len(path) > 64*1024 || len(payload) > 1024*1024*1024 {
		panic("crazy length") // TODO
	}
	message := make([]byte, 4+len(path)+len(payload)) // 4 bytes is excessive worst case for path length.
	n := binary.PutUvarint(message, uint64(len(path)))
	copy(message[n:], path)
	copy(message[n+len(path):], payload)
	message = message[:n+len(path)+len(payload)]
	// Lazy encryption. TODO.
	for i, c := range message {
		message[i] = c ^ 0x55
	}
	return message
}

// unpackBlob decrypts the data in place and returns the path name and data.
func unpackBlob(data []byte) (PathName, []byte, error) {
	if len(data) > 64*1024+1024*1024*1024 {
		return "", nil, errors.New("crazy length") // TODO
	}
	// Lazy decryption. TODO.
	for i, c := range data {
		data[i] = c ^ 0x55
	}
	nameLen, n := binary.Uvarint(data)
	if n == 0 {
		return "", nil, errors.New("buf too small") // TODO
	}
	if n < 0 {
		return "", nil, errors.New("namelen overflow") // TODO
	}
	if nameLen > 64*1024 {
		return "", nil, errors.New("decoded namelen too long") // TODO
	}
	data = data[n:]
	if len(data) < int(nameLen) {
		return "", nil, errors.New("parse error; name too short") // TODO
	}
	name, payload := data[:nameLen], data[nameLen:]
	return PathName(name), payload, nil
}

// StorageService returns data and metadata referenced by the request.
type StorageService struct {
	Location Location
	blob     map[Reference]*Blob
}

func NewStorageService(loc Location) *StorageService {
	return &StorageService{
		Location: loc,
		blob:     make(map[Reference]*Blob),
	}
}

type Blob struct {
	data     []byte
	hash     Hash
	metadata []byte // Not sure what this looks like; includes keys, owner, ???
}

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

// TODO: Should it return a HintedReference?
func (ss *StorageService) Put(ciphertext []byte) (ref Reference, err error) {
	hash := HashOf(ciphertext)
	ref = Reference{
		hash,
	}
	ss.blob[ref] = &Blob{
		copyOf(ciphertext),
		hash,
		[]byte("metadata"), // TODO: probably want defaults.
	}
	return ref, nil
}

// TODO: API should provide alternate location if missing.
func (ss *StorageService) Get(ref Reference) (ciphertext []byte, err error) {
	blob, ok := ss.blob[ref]
	if !ok {
		return nil, errors.New("no such blob")
	}
	if HashOf(blob.data) != blob.hash {
		return nil, errors.New("internal hash mismatch in StorageService.Get")
	}
	if ref.Hash != blob.hash {
		return nil, errors.New("external hash mismatch in StorageService.Get")
	}
	return copyOf(blob.data), nil
}

func (ss *StorageService) GetMetadata(ref Reference) (cleartext []byte, err error) {
	blob, ok := ss.blob[ref]
	if !ok {
		return nil, errors.New("no such blob")
	}
	if HashOf(blob.data) != blob.hash {
		return nil, errors.New("internal hash mismatch in GetMetadata")
	}
	return copyOf(blob.metadata), nil
}
