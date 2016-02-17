package upspin

// A UserName is just a string representing a user.
// It is given a unique type so the API is clear.
// Example: gopher@google.com
type UserName string

// A PathName is just a string representing a full path name.
// It is given a unique type so the API is clear.
// Example: gopher@google.com/burrow/hoard
type PathName string

// Transport identifies the type of access required to reach the data, that is, the
// realm in which the network address within a Location is to be interpreted.
type Transport uint8

const (
	// InProcess indicates that contents are located in the current process,
	// typically in memory.
	InProcess Transport = iota

	// GCP indicates a Google Cloud Store instance.
	GCP

	// HTTP is a URL-encoded address.
	HTTP
)

// A Location identifies where a piece of data is stored and how to retrieve it.
type Location struct {
	// Endpoint identifies the machine or service where the data resides.
	Endpoint Endpoint

	// Reference is the key that will retrieve the data from the endpoint.
	Reference Reference
}

// An Endpoint identifies an instance of a service, encompassing an address
// such as a domain name and information (the Transport) about how to interpret
// that address.
type Endpoint struct {
	// Transport specifies how the network address is to be interpreted,
	// for instance that it is the URL of an HTTP service.
	Transport Transport

	// NetAddr returns the (typically) network address of the data.
	NetAddr NetAddr
}

// A NetAddr is the network address of service. It is interpreted by Access's
// Dial method to connect to the service.
type NetAddr string

// A Reference is the key to find a piece of data in a given Store. It is decoupled
// from the address of the Store itself, but contains a unique identifier key
// such as a hash of the contents and a Packing defining how to unpack it.
type Reference struct {
	// Key identifies the data within the Store.
	Key string

	// Packing identifies how to unpack the original data using this Reference.
	Packing Packing
}

// A Packing identifies the technique for turning the data pointed to by
// a Reference into the user's data. This may involve checksum verification,
// decrypting, signature checking, or nothing at all.
// Secondary data such as encryption keys may be required to implement
// the packing. That data appears in the API as arguments and struct fields
// called packdata.
type Packing uint8

// Packer provides the implementation of a Packing. The pack package binds
// Packing values to the concrete implementations of this interface.
type Packer interface {
	// Packing returns the integer identifier of this Packing algorithm.
	Packing() Packing

	// Pack takes cleartext data, metadata and path name and
	// stores the ciphertext encoding in the supplied slice.
	// The ciphertext and cleartext slices must not overlap.
	// Pack might update the metadata.
	// The slice must be large enough; the PackLen method may be used to
	// find a suitable allocation size.
	// The returned count is the length of the ciphertext.
	Pack(ciphertext, cleartext []byte, meta *Metadata, name PathName) (int, error)

	// Unpack takes ciphertext data and packing metadata and stores the
	// cleartext version in the supplied slice, which must be large enough.
	// The ciphertext and cleartext slices must not overlap.
	// Unpack might update the metadata.
	// It returns the path name and the number of bytes written to the slice.
	// The returned name will be empty if the ciphertext does not contain one.
	Unpack(cleartext, ciphertext []byte, meta *Metadata, name PathName) (int, error)

	// PackLen returns an upper bound on the number of bytes required
	// to store the cleartext after packing. It might update the metadata.
	// Returns -1 if there is an error.
	PackLen(cleartext []byte, meta *Metadata, name PathName) int

	// UnpackLen returns an upper bound on the number of bytes required
	// to store the unpacked cleartext. It might update the metadata.
	// Returns -1 if there is an error.
	UnpackLen(ciphertext []byte, meta *Metadata) int
}

const (
	// The DebugPack packing is available for use in tests for any purpose.
	// It is never used in production.
	DebugPack Packing = iota

	// PlainPack is the trivial, no-op packing. Bytes are copied untouched.
	PlainPack

	// EEp256Pack packing stores AES-encrypted data.
	// The associated metadata has an ECDSA signature and ECDH-wrapped keys.
	EEp256Pack
)

// User service.

// The User interface provides access to public information about users.
type User interface {
	Access

	// Lookup returns a list (slice) of Endpoints of Directory services that may
	// hold the root directory for the named user. Those earlier in the
	// list are better places to look.
	Lookup(userName UserName) ([]Endpoint, error)
}

// Directory service.

// The Directory service manages the name space for one or more users.
type Directory interface {
	Access

	// Lookup returns the directory entry for the named file.
	Lookup(name PathName) (*DirEntry, error)

	// Put stores the data at the given name and associates packdata with it.
	// If something is already stored with that name, the new data and
	// packdata replace the old.
	Put(name PathName, data []byte, packdata []byte) (Location, error)

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
	Metadata Metadata
}

// Metadata stores (among other things) the keys that enable the
// file to be decrypted by the appropriate recipient.
type Metadata struct {
	IsDir    bool   // The file is a directory.
	Sequence int64  // The sequence (version) number of the item.
	PackData []byte // Packing-specific metadata stored in directory.
}

// Store service.

// The Store service saves and retrieves data without interpretation.
type Store interface {
	Access

	// Get attempts to retrieve the data identified by the key.
	// Three things might happen:
	// 1. The data is in this Store. It is returned. The Location slice
	// and error are nil.
	// 2. The data is not in this Store, but may be in one or more
	// other locations known to the store. The slice of Locations
	// is returned. The data slice and error are nil.
	// 3. An error occurs. The data and Location slices are nil
	// and the error describes the problem.
	Get(key string) ([]byte, []Location, error)

	// Put puts the data into the store and returns the key
	// to be used to retrieve it.
	Put(data []byte) (string, error)

	// Delete permanently removes all storage space associated
	// with key. After a successful Delete, calls to Get with the
	// same key will fail. If a key is not found, an error is
	// returned.
	Delete(key string) error

	// Endpoint returns the network endpoint of the server.
	Endpoint() Endpoint
}

// Client API.

// The Client interface provides a higher-level API suitable for applications
// that wish to access Upspin's name space.
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
	Put(name PathName, data []byte) (Location, error)

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

// ClientContext contains information useful to the client such the user's
// keys and preferred User, Directory, and Store servers.
// TODO: Should this be an interface instead?
type ClientContext struct {
	// PrivateKey holds the user's private cryptographic keys.
	// TODO: what type should this field be?
	PrivateKey interface{}

	// PublicKey holds the user's private cryptographic keys.
	// TODO: what type should this field be?
	PublicKey interface{}

	// Packing is the Packing to use when creating new data items.
	Packing Packing

	// User is the User service to contact when evaluating names.
	User User

	// Directory is the Directory in which to place new data items,
	// usually the location of the user's root.
	Directory Directory

	// Store is the Store in which to place new data items.
	Store Store
}

// Access defines how to connect and authenticate to a server. Each
// service type (User, Directory, Store) implements the methods of
// the Access interface. These methods are not used directly by
// clients. Instead, clients should use the various Bind methods of
// the Upspin "access" package to connect to services.
type Access interface {
	// Dial connects to the service and performs any needed authentication.
	Dial(*ClientContext, Endpoint) (interface{}, error)

	// ServerUserName returns the authenticated user name of the server.
	// If there is no authenticated name an empty string is returned.
	// TODO(p): Should I distinguish a server which didn't pass authentication
	// from one which has no user name?
	ServerUserName() string
}
