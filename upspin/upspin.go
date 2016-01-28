// Package upspin contains global interface and other definitions for the components of the system.
package upspin

import "net"

// A Packing identifies the technique for turning the data pointed to by
// a reference into the user's data. This may involve checksum verification,
// decrypting, signature checking, or nothing at all.
// Secondary data, metadata, may be required to implement the packing.
type Packing uint8

// TODO: These constants are just placeholders.
const (
	// The Debug packing is available for use in tests for any purpose. Never used in production.
	Debug Packing = iota

	// HTTP uses a URL as a reference. TODO: This isn't about the packing at all.
	HTTP

	// The EllipticalEric packing stores data using encryption defined by XXX.
	EllipticalEric
)

// A Location describes how to retrieve a piece of data (a "blob") from the Store service.
type Location struct {
	// AccessMethod name.
	AccessMethodName string

	// NetAddr returns the network address of the data.
	NetAddr NetAddr

	// Reference returns the reference information for the data.
	Reference Reference
}

// Marshal packs the Location into a byte slice for transport.
func (Location) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Location.
func (Location) Unmarshal([]byte) error {
	panic("unimplemented")
}

// A NetAddr is a network address.
// It probably isn't just a net.Addr, but that will do for now.
// Perhaps it's even just a piece of text.
type NetAddr struct {
	net.Addr
}

// A Reference is the key to find a piece of data in a Store. It is decoupled
// from the address of the Store itself, but contains a unique identifier key
// such as a hash of the contents and a Packing defining how to unpack it.
type Reference struct {
	// Key identifies the data.
	Key string

	// Packing identifies how to unpack the original data using this Reference.
	Packing Packing
}

// Marshal packs the Reference into a byte slice for transport.
func (Reference) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Reference.
func (Reference) Unmarshal([]byte) error {
	panic("unimplemented")
}

// A UserName is just a string representing a user, but given a unique type so the API is clear.
// Example: gopher@google.com
type UserName string

// A PathName is just a string representing a full path name, but given a unique type so the API is clear.
// Example: gopher@google.com/burrow/hoard
type PathName string

// User service.

type User interface {
	// Lookup returns a list of addresses of Directory services that may
	// have the root directory for the named user. Those earlier in the
	// list are better places to look.
	Lookup(userName UserName) ([]NetAddr, error)
}

// Directory service.

// The Directory service manages the name space for one or more users.
type Directory interface {
	// Lookup returns the directory entry for the named file.
	Lookup(name PathName) (*DirEntry, error)

	// Put stores the data at the given name. If something is already
	// stored with that name, it is replaced with the new data.
	Put(name PathName, data []byte) (Location, error)

	// MakeDirectory creates a directory with the given name, which
	// must not already exist. All but the last element of the path name
	// must already exist and be directories.
	// TODO: Make multiple elems?
	MakeDirectory(dirName PathName) (Location, error)

	// Glob matches the pattern against the file names of the full rooted tree.
	// That is, the pattern must look like a full path name, but elements of the
	// path may contain metacharacters. Matching is done using Go's path.Match
	// elementwise. The user name must be present in the pattern and is treated as
	// a literal even if it contains metacharacters.
	// The Metadata contains no key information.
	Glob(pattern string) ([]*DirEntry, error)
}

// DirEntry represents the directory information for a file.
type DirEntry struct {
	Name     PathName // The full path name of the file.
	Location Location // The location of the file.
	Metadata Metadata // Metadata, including wrapped keys for decrypting file (if necessary).
}

// Metadata stores (among other things) the keys that enable the
// file to be decrypted by the appropriate recipient.
type Metadata struct {
	IsDir       bool   // The file is a directory.
	Sequence    int64  // The sequence (version) number of the item.
	Signature   []byte // The ECDSA signature of path name and contents.
	WrappedKeys []WrappedKey
}

// A WrappedKey holds a key that will decrypt the file contents. The key is in turn
// encrypted with some user's private key. The 16-bit XOR of the user's public
// key is stored alongside to make it easier to find which key to use if many are
// present.
type WrappedKey struct {
	Hash      [2]byte // 16-bit hash of public key for user.
	Encrypted []byte  // Key used to decrypt contents, itself encrypted with public key for user.
}

// Store service.

// The Store service saves and retrieves data without interpretation.
type Store interface {
	// Get attempts to retrieve the data stored at the Location.
	// Three things might happen:
	// 1. The data is in this Store. It is returned. The Location slice
	// and error are nil.
	// 2. The data is not in this Store, but may be in one or more
	// other locations known to the store. The slice of Locations
	// is returned. The data slice and error are nil.
	// 3. An error occurs. The data and Location slices are nil
	// and the error describes the problem.
	// TODO: Does argument Location need to refer to this Store?
	Get(location Location) ([]byte, []Location, error)

	// Put puts the data into the store. If the packing for the
	// Reference involves a content-addressable key, the
	// value computed from the data must match the supplied
	// Reference and the Put may return an error if
	// the data is already known. Otherwise the value stored
	// under the Reference is replaced.
	Put(ref Reference, data []byte) (Location, error)

	// NetAddr returns the network address of the server.
	NetAddr() NetAddr
}

// Client API.

type Client interface {
	// Get returns the clear, decrypted data stored under the given name.
	// It is intended only for special purposes, since it will allocate memory
	// for the entire "blob" to return. Most access will use the file-like
	// API below.
	Get(name PathName) ([]byte, error)

	// Put stores the data at the given name. If something is already
	// stored with that name, it is replaced with the new data.
	// Like Get, it is not the usual access method. The file-like API
	// is preferred.
	// TODO: How is metadata handled?
	Put(name PathName, data, metadata []byte) (Location, error)

	// MakeDirectory creates a directory with the given name, which
	// must not already exist. All but the last element of the path name
	// must already exist and be directories.
	// TODO: Make multiple elems?
	MakeDirectory(dirName PathName) (Location, error)

	// File-like methods similar to Go's os.File API.
	// The name, however, is a fully-qualified upspin PathName.
	// TODO: Should there be a concept of current directory and
	// local names?
	Create(name PathName) (File, error)
	Open(name PathName) (File, error)
}

// The File interface has semantics and API that parallels a subset
// of Go's os.File's. The main semantic difference, besides the limited
// method set, is that a Read will only return once the entire contents
// have been decrypted and verified.
type File interface {
	// Close releases the resources. For a writable file, it also
	// writes the accumulated data in a Store server. After a
	// Close, successful or not, all methods of File except Name
	// will fail.
	Close() error

	// Name returns the full path name of the File.
	Name() PathName

	// Read, ReadAt, Write, WriteAt and Seek implement
	// the standard Go interfaces io.Reader, etc.
	// Because of the nature of upsin storage, the entire
	// item might need to be read into memory by the
	// implementation before Read can return any data.
	// Similarly, Write might accumulate all data and only
	// flush to storage once Close is called.
	Read(b []byte) (n int, err error)
	ReadAt(b []byte, off int64) (n int, err error)
	Write(b []byte) (n int, err error)
	WriteAt(b []byte, off int64) (n int, err error)
	Seek(offset int64, whence int) (ret int64, err error)
}

// ClientContext contains information about the client, e.g., name and how to
// access private keys.  Specifics will be filled in as we decide more about
// security/encryption.
type ClientContext interface {
	Name() string
}

// An AccessMethod defines a specific set of upspin interfaces.  Each AccessMethod
// corresponds to a type of implementation; in process, networked using TLS over HTTP, etc.
// If the AccessMethod requires a network, then this interface points to stubs to protect
// and marshal the data across the network.
type AccessMethod interface {
	// The stubs (or direct calls) for the service implementations.
	Directory
	Store
	User

	// Connect connects to the service and returns an AccessMethod instance that
	// contains the connection information.
	Connect(Location) (AccessMethod, error)

	// ServerUserName returns the authenticated user name of the server.
	// If there is none, or the server is not authenticated, <THIS HAPPENS>.
	ServerUserName() string
}

// An AccessSwitch manages AccessMethods. There will be only one global AccessSwitch
// per process and each AccessMethod linked into the binary will use its Init function
// to install itself in the AccessSwitch.
type AccessSwitch interface {
	// Install installs an AccessMethod.
	Install(string, AccessMethod)

	// Bind looks up the AccessMethod using the name in Location, calls its Connect routine.
	Bind(ClientContext, Location) (AccessMethod, error)
}
