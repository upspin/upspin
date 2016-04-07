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

// A NetAddr is the network address of service. It is interpreted by Dialer's
// Dial method to connect to the service.
type NetAddr string

// A Reference is the string identifying an item in a Store.
type Reference string

// PackData stores the encoded information used to pack the data in an
// item, such decryption keys. The first byte identifies the Packing
// used to store the information; the rest of the slice is the data
// itself.
type PackData []byte

// A Packing identifies the technique for turning the data pointed to by
// a key into the user's data. This may involve checksum verification,
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

	// String returns the name of this packer.
	String() string

	// Pack takes cleartext data, metadata and path name and
	// stores the ciphertext encoding in the supplied slice.
	// The ciphertext and cleartext slices must not overlap.
	// Pack might update the metadata, which must not be
	// nil but might have a nil PackData field. If meta.PackData has length>0,
	// the first byte must be the correct value of Packing.
	// The slice must be large enough; the PackLen method may be used to
	// find a suitable allocation size.
	// The returned count is the length of the ciphertext.
	Pack(context *Context, ciphertext, cleartext []byte, meta *Metadata, name PathName) (int, error)

	// Unpack takes ciphertext data and packing metadata and stores the
	// cleartext version in the supplied slice, which must be large enough.
	// The ciphertext and cleartext slices must not overlap.
	// Unpack might update the metadata, which must have the correct Packing
	// value already present in meta.PackData[0].
	// It returns the path name and the number of bytes written to the slice.
	// The returned name will be empty if the ciphertext does not contain one.
	Unpack(context *Context, cleartext, ciphertext []byte, meta *Metadata, name PathName) (int, error)

	// PackLen returns an upper bound on the number of bytes required
	// to store the cleartext after packing.
	// PackLen might update the metadata, which must not be
	// nil but might have a nil PackData field. If meta.PackData has length>0,
	// the first byte must be the correct value of Packing.
	// Returns -1 if there is an error.
	PackLen(context *Context, cleartext []byte, meta *Metadata, name PathName) int

	// UnpackLen returns an upper bound on the number of bytes required
	// to store the unpacked cleartext.
	// UnpackLen might update the metadata, which must have the correct Packing
	// value already present in meta.PackData[0].
	// Returns -1 if there is an error.
	UnpackLen(context *Context, ciphertext []byte, meta *Metadata) int
}

const (
	// PlainPack is the trivial, no-op packing. Bytes are copied untouched.
	// It is the default packing but is, of course, insecure.
	PlainPack Packing = 0

	// Packings from 1 through 16 are not for production use. This region
	// is reserved for debugging and other temporary packing implementations.

	// DebugPack is available for use in tests for any purpose.
	// It is never used in production.
	DebugPack Packing = 1

	// Packings from 16 and above (as well as PlainPack=0) are fixed in
	// value and semantics and may be used in production.

	// EEp256Pack and EEp521Pack store AES-encrypted data, with metadata
	// including an ECDSA signature and ECDH-wrapped keys.
	// See NIST SP 800-57 Pt.1 Rev.4 section 5.6.1
	// EEp256Pack uses AES-128, SHA-256, and curve P256; strength 128.
	EEp256Pack Packing = 16
	// EEp384Pack uses AES-256, SHA-512, and curve P384; strength 192.
	EEp384Pack Packing = 18
	// EEp521Pack uses AES-256, SHA-512, and curve P521; strength 256.
	EEp521Pack Packing = 17

	Ed25519Pack Packing = 19 // TODO(ehg) x/crytpo/curve25519, github.com/agl/ed25519
)

// User service.

// The User interface provides access to public information about users.
type User interface {
	Dialer

	// Lookup returns a list (slice) of Endpoints of Directory
	// services that may hold the root directory for the named
	// user and a list (slice) of public keys for that user. Those
	// earlier in the lists are better places to look.
	Lookup(userName UserName) ([]Endpoint, []PublicKey, error)
}

// A PublicKey can be given to anyone and used for authenticating a User.
type PublicKey string

// A PrivateKey is paired with PublicKey but never leaves the Client.
type PrivateKey string

// A KeyPair is used when exchanging data with other users. It
// always contains both the public and private keys.
type KeyPair struct {
	Public  PublicKey
	Private PrivateKey
}

// Directory service.

// The Directory service manages the name space for one or more users.
type Directory interface {
	Dialer

	// Lookup returns the directory entry for the named file.
	Lookup(name PathName) (*DirEntry, error)

	// Put stores the location of data at the given path and associates packdata with it.
	// It is assumed that the data is available at that location and that
	// the packdata is correct.
	// All but the last element of the path name must already exist and be
	// directories. The final element, if it exists, must not be a directory.
	// If something is already stored under the path, the new location and
	// packdata replace the old. If PutOptions is nil, the behavior of the options
	// is equivalent  to the default behavior for each option value.
	// See the documentation for PutOptions for more information.
	Put(path PathName, loc Location, packdata PackData, opts *PutOptions) error

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

// PutOptions contains several optional data items relevant to the operation
// of Directory.Put.
// For each field, the zero value represents the default behavior.
type PutOptions struct {
	// Size represents the size of the original, unpacked data as seen by
	// the client. It is stored in the Size field of the metadata for the new item.
	// It is advisory only and is unchecked. The default value of Size is zero.
	Size uint64

	// Time represents a timestamp for the item. It is stored in the Time
	// field of the metadata for the new item. It is advisory only and is
	// unchecked. If the value is zero, the Directory service instead
	// records the time of the operation.
	Time Time

	// Sequence represents a sequence number for the operation. If it
	// is non-zero, the Directory service will reject the Put operation
	// unless Sequence is the same as that currently stored in the metadata
	// for the item.
	Sequence int64
}

// Time represents a timestamp in units of seconds since
// the Unix epoch, Jan 1 1970 0:00 UTC.
type Time int64

// DirEntry represents the directory information for a file.
type DirEntry struct {
	Name     PathName // The full path name of the file.
	Location Location // The location of the file.
	Metadata Metadata
}

// Metadata stores (among other things) the keys that enable the
// file to be decrypted by the appropriate recipient.
type Metadata struct {
	IsDir    bool       // The file is a directory.
	Sequence int64      // The sequence (version) number of the item.
	Size     uint64     // Length of file in bytes.
	Time     Time       // Time associated with file; might be when it was last written.
	Readers  []UserName // Users (or groups) allowed to read this entry (only used if IsDir).
	PackData []byte     // Packing-specific metadata stored in directory.
}

// Store service.

// The Store service saves and retrieves data without interpretation.
type Store interface {
	Dialer

	// Get attempts to retrieve the data identified by the reference.
	// Three things might happen:
	// 1. The data is in this Store. It is returned. The Location slice
	// and error are nil.
	// 2. The data is not in this Store, but may be in one or more
	// other locations known to the store. The slice of Locations
	// is returned. The data slice and error are nil.
	// 3. An error occurs. The data and Location slices are nil
	// and the error describes the problem.
	Get(ref Reference) ([]byte, []Location, error)

	// Put puts the data into the store and returns the reference
	// to be used to retrieve it.
	Put(data []byte) (Reference, error)

	// Delete permanently removes all storage space associated
	// with the reference. After a successful Delete, calls to Get with the
	// same key will fail. If a key is not found, an error is
	// returned.
	Delete(ref Reference) error

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

	// Glob matches the pattern against the file names of the full rooted tree.
	// That is, the pattern must look like a full path name, but elements of the
	// path may contain metacharacters. Matching is done using Go's path.Match
	// elementwise. The user name must be present in the pattern and is treated as
	// a literal even if it contains metacharacters.
	// The Metadata contains no key information.
	Glob(pattern string) ([]*DirEntry, error)

	// File-like methods similar to Go's os.File API.
	// The name, however, is a fully-qualified upspin PathName.
	// TODO: Should there be a concept of current directory and
	// local names?
	Create(name PathName) (File, error)
	Open(name PathName) (File, error)

	// Directory returns an error or a reachable bound Directory for the user.
	Directory(name PathName) (Directory, error)

	// Directory returns an error or a slice of public keys for the user.
	PubKeys(name PathName) ([]PublicKey, error)
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

// Context contains client information such as the user's keys and
// preferred User, Directory, and Store servers.
type Context struct {
	// The name of the user requesting access.
	UserName UserName

	// KeyPair holds the user's private cryptographic keys.
	KeyPair KeyPair

	// Packing is the default Packing to use when creating new data items.
	// It may be overridden by circumstances such as preferences related
	// to the directory.
	Packing Packing

	// User is the User service to contact when evaluating names.
	User         User
	UserEndpoint Endpoint

	// Directory is the Directory in which to place new data items,
	// usually the location of the user's root.
	Directory         Directory
	DirectoryEndpoint Endpoint

	// Store is the Store in which to place new data items.
	Store         Store
	StoreEndpoint Endpoint
}

// Dialer defines how to connect and authenticate to a server. Each
// service type (User, Directory, Store) implements the methods of
// the Dialer interface. These methods are not used directly by
// clients. Instead, clients should use the methods of
// the Upspin "bind" package to connect to services.
type Dialer interface {
	// Dial connects to the service and performs any needed authentication.
	Dial(*Context, Endpoint) (interface{}, error)

	// ServerUserName returns the authenticated user name of the server.
	// If there is no authenticated name an empty string is returned.
	// TODO(p): Should I distinguish a server which didn't pass authentication
	// from one which has no user name?
	ServerUserName() string
}
