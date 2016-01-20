// Package upspin contains global interface and other definitions for the components of the system.
package upspin

import "net"

// A Protocol identifies the technique for turning a reference into the user's data.
// Secondary data, metadata, may be required to implement the protocol.
type Protocol int

const (
	// The HTTP protocol uses a URL as a reference.
	HTTP Protocol = iota

	// The EllipticalEric protocol stores data using encryption defined by XXX.
	EllipticalEric
)

// A Location describes how to retrieve a piece of data (a "blob") from the Store service.
type Location interface {
	// NetAddr returns the network address of the data.
	NetAddr() NetAddr

	// Reference returns the reference information for the data.
	Reference() Reference

	// Marshal packs the Location into a byte slice for transport.
	Marshal([]byte) error

	// Unmarshal unpacks the byte slice to recover the encoded Location.
	Unmarshal([]byte) error
}

// A NetAddr is a network address.
// It probably isn't just a net.Addr, but that will do for now.
// Perhaps it's even just a piece of text.
type NetAddr struct {
	net.Addr
}

// A Reference is the key to find a piece of data in a Store. It is decoupled
// from the address of the Store itself, but contains a unique identifier key
// such as a hash of the contents and a Protocol defining how to unpack it.
type Reference interface {
	// Key identifies the data.
	Key() []byte

	// Protocol identifies how to recover the original data using this Reference.
	Protocol() Protocol

	// Marshal packs the Reference into a byte slice for transport.
	Marshal([]byte) error

	// Unmarshal unpacks the byte slice to recover the encoded Reference.
	UnMarshal([]byte) error
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
	Get(name PathName) ([]Location, error)

	// Put stores the data at the given name. If something is already
	// stored with that name, it is replaced with the new data.
	// TODO: How is metadata handled?
	Put(name PathName, data, metadata []byte) (Location, error)

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
	Glob(pattern string) ([]DirEntry, error)
}

type DirEntry struct {
	Name     string // The full path name of the item.
	Location Location
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

	// Put puts the data into the store. If the protocol for the
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
	Get(name PathName) ([]byte, error)

	// TODO: An alternate design. Could be used in Directory as well.
	// Get_ returns the clear, decrypted data stored under the given name.
	// It returns the number of bytes stored in the data and the
	// length of the full item.
	// If the data slice is not sufficient to store the full item, the data
	// slice will be filled, the error will be nil, but the second count
	// will store the full slice so a retry may be made.
	Get_(name PathName, data []byte) (int64, int64, error)

	// Put stores the data at the given name. If something is already
	// stored with that name, it is replaced with the new data.
	// TODO: How is metadata handled?
	Put(name PathName, data, metadata []byte) (Location, error)

	// MakeDirectory creates a directory with the given name, which
	// must not already exist. All but the last element of the path name
	// must already exist and be directories.
	// TODO: Make multiple elems?
	MakeDirectory(dirName PathName) (Location, error)
}
