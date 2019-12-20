// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin // import "upspin.io/upspin"

import (
	"crypto/elliptic"
	"errors"
	"math/big"
	"time"
)

// A UserName is just an e-mail address representing a user.
// It is given a unique type so the API is clear.
// The user part may contain an optional suffix after a plus sign.
// Examples: gopher@google.com, me+you@forever.com
type UserName string

// A PathName is just a string representing a full path name.
// It is given a unique type so the API is clear.
// Example: gopher@google.com/burrow/hoard
type PathName string

// Transport identifies the type of access required to reach the data, that is, the
// realm in which the network address within a Location is to be interpreted.
type Transport uint8

const (
	// Unassigned denotes a connection to a service that returns an error
	// from every method call. It is useful when a component wants to
	// guarantee it does not access another service.
	// It is also the zero value for Transport.
	Unassigned Transport = iota

	// InProcess denotes that contents are located in the current process,
	// typically in memory.
	InProcess

	// Remote denotes a connection to a remote server through RPC.
	// (Although called remote, the service may be running on the same machine.)
	// The Endpoint's NetAddr contains the HTTP address of the server.
	Remote
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

// A Reference is the string identifying an item in a StoreServer. It must
// be a valid UTF-8-encoded Unicode string and not contain U+FFFD.
type Reference string

// These special references are used to obtain out-of-band information from
// StoreServer implementations. StoreServers are not required to support them,
// in which case they may return errors.NotExist.
var (
	// HealthMetadata is used to check that a StoreServer is up. Servers
	// need not return any special response when asked for this reference.
	HealthMetadata Reference = "metadata:Health"

	// HTTPBaseMetadata is used to obtain a base URL from which references
	// may be requested by HTTP(S). The server may return a URL to which
	// a reference may be appended to obtain that reference's data.
	HTTPBaseMetadata Reference = "metadata:HTTP-Base"

	// FlushWritebacksMetadata is used as a signal to flush the cache.
	// A Get will return only after all writebacks have completed.
	FlushWritebacksMetadata Reference = "metadata:FlushWritebacks"

	// ListRefsMetadata is used by administrators to enumerate the
	// references held by a StoreServer. Callers pass this value verbatim
	// for the initial request and append a pagination token for subsequent
	// requests. The response from such a request is a JSON-encoded
	// ListRefsResponse.
	ListRefsMetadata Reference = "metadata:ListRefs:"
)

// ListRefsResponse describes a response from a StoreServer.Get
// call for ListRefsMetadata.
type ListRefsResponse struct {
	// Refs holds the reference information.
	Refs []ListRefsItem
	// Next holds the token to fetch the next page,
	// or the empty string if this is the last page.
	Next string
}

// ListRefsItem describes a reference in a StoreServer,
// returned as part of a ListRefsResponse.
type ListRefsItem struct {
	// Ref holds the reference name.
	Ref Reference
	// Size the length of the reference data.
	Size int64
}

// Signature is an ECDSA signature.
type Signature struct {
	R, S *big.Int
}

// DEHash is a hash of the Name, Link, Attribute, Packing, and Time
// fields of a DirEntry. When using EE or EEIntegrity the hash also includes the
// block checksums; when using EE it includes the block encryption key.
type DEHash []byte

// Factotum implements an agent, potentially remote, to handle private key operations.
type Factotum interface {
	// DirEntryHash is a summary used in signing and verifying directory entries.
	DirEntryHash(n, l PathName, a Attribute, p Packing, t Time, dkey, hash []byte) DEHash

	// FileSign ECDSA-signs DirEntry fields and, in some packings, file contents.
	FileSign(hash DEHash) (Signature, error)

	// ScalarMult is the bare private key operator, used in unwrapping packed data.
	// Each call needs security review to ensure it cannot be abused as a signing
	// oracle. Read https://en.wikipedia.org/wiki/Confused_deputy_problem.
	ScalarMult(keyHash []byte, c elliptic.Curve, x, y *big.Int) (sx, sy *big.Int, err error)

	// Sign signs a slice of bytes with the factotum's private key.
	// The argument hash should be a cryptographic hash of the message you want to sign,
	// no longer than your key's curve order. Don't use without a security consult.
	Sign(hash []byte) (Signature, error)

	// HKDF cryptographically mixes salt, info, and the Factotum secret and
	// writes the result to out, which may be of any length but is typically
	// 8 or 16 bytes. The result is unguessable without the secret, and does
	// not leak the secret. For more information, see package
	// golang.org/x/crypto/hkdf.
	HKDF(salt, info, out []byte) error

	// Pop derives a Factotum that defaults to the previous key.
	Pop() Factotum

	// PublicKey returns the user's public key in canonical string format.
	PublicKey() PublicKey

	// PublicKeyFromHash returns the matching public key or an error.
	PublicKeyFromHash(keyHash []byte) (PublicKey, error)
}

// A Packing identifies the technique for turning the data pointed to by
// a key into the user's data. This may involve checksum verification,
// decrypting, signature checking, or nothing at all.
// Secondary data such as encryption keys may be required to implement
// the packing. That data appears in the API as arguments and struct fields
// called packdata.
type Packing uint8

// BlockPacker operates on a DirEntry, packing and signing DirBlocks.
type BlockPacker interface {
	// Pack takes cleartext data and returns the packed ciphertext,
	// appending a DirBlock to the DirEntry.
	//
	// The ciphertext slice remains valid until the next call to Pack.
	Pack(cleartext []byte) (ciphertext []byte, err error)

	// SetLocation updates the Location field of the last-Packed DirBlock.
	SetLocation(Location)

	// Close updates the Signature for the DirEntry and releases any
	// resources associated with the packing operation.
	Close() error
}

// BlockUnpacker operates on a DirEntry, unpacking and verifying its DirBlocks.
type BlockUnpacker interface {
	// NextBlock returns the next DirBlock in the sequence and true,
	// or a zero DirBlock and false if there are no more blocks to unpack.
	NextBlock() (DirBlock, bool)

	// SeekBlock returns the nth DirBlock and true,
	// or a zero DirBlock and false if the nth block does not exist.
	SeekBlock(n int) (DirBlock, bool)

	// Unpack takes ciphertext returns the cleartext. If appropriate, the
	// result is verified as correct according to the block's Packdata.
	//
	// The cleartext slice remains valid until the next call to Unpack.
	Unpack(ciphertext []byte) (cleartext []byte, err error)

	// Close releases any resources associated with the unpacking
	// operation.
	Close() error
}

// Packer provides the implementation of a Packing. The pack package binds
// Packing values to the concrete implementations of this interface.
type Packer interface {
	// Packing returns the integer identifier of this Packing algorithm.
	Packing() Packing

	// String returns the name of this packer.
	String() string

	// Pack returns a BlockPacker that packs blocks
	// into the given DirEntry.
	Pack(Config, *DirEntry) (BlockPacker, error)

	// Unpack returns a BlockUnpacker that unpacks blocks
	// from the given DirEntry.
	Unpack(Config, *DirEntry) (BlockUnpacker, error)

	// PackLen returns an upper bound on the number of bytes required
	// to store the cleartext after packing.
	// PackLen might update the entry's Packdata field.
	// PackLen returns -1 if there is an error.
	PackLen(config Config, cleartext []byte, entry *DirEntry) int

	// UnpackLen returns an upper bound on the number of bytes
	// required to store the unpacked cleartext.
	// UnpackLen might update the entry's Packdata field.
	// UnpackLen returns -1 if there is an error.
	UnpackLen(config Config, ciphertext []byte, entry *DirEntry) int

	// ReaderHashes returns SHA-256 hashes of the public keys able to decrypt the
	// associated ciphertext.
	ReaderHashes(packdata []byte) ([][]byte, error)

	// Share updates each packdata element to enable all the readers,
	// and only those readers, to be able to decrypt the associated ciphertext,
	// which is held separate from this call. It is used to repair incorrect
	// key wrapping after access rights are changed.
	// In case of error, Share skips processing for that reader or packdata.
	// If packdata[i] is nil on return, it was skipped.
	// Share trusts the caller to check the arguments are not malicious.
	// To enable all Upspin users to decrypt the ciphertext, include
	// AllReadersKey among the provided reader keys.
	//
	// TODO: It would be nice if DirServer provided a method to report
	// which items need updates, so this could be automated.
	Share(config Config, readers []PublicKey, packdata []*[]byte)

	// Name updates the DirEntry to refer to a new path. If the new
	// path is in a different directory, the wrapped keys are reduced to
	// only that of the Upspin user invoking the method. The Packdata
	// in entry must contain a wrapped key for that user.
	Name(config Config, entry *DirEntry, path PathName) error

	// SetTime changes the Time field in a DirEntry and recomputes
	// its signature.
	SetTime(config Config, entry *DirEntry, time Time) error

	// Countersign updates the signatures in the DirEntry when a writer
	// is in the process of switching to a new key. It checks that
	// the first existing signature verifies under the old key, copies
	// that one over the second existing signature, and creates a new
	// first signature using the key from factotum.
	Countersign(oldKey PublicKey, f Factotum, d *DirEntry) error

	// UnpackableByAll reports whether the packed data may be unpacked by
	// all Upspin users. Access and Group files must have this property, as
	// should any files for which access.AllUsers have the read permission.
	UnpackableByAll(d *DirEntry) (bool, error)
}

const (
	// UnassignedPack is not a packer, but a special value indicating no
	// packer was chosen. It is an error to use this value in a DirEntry
	// except when creating a directory, in which case the DirServer will
	// assign the proper packing.
	UnassignedPack Packing = 0

	// PlainPack is a no-encryption, no-integrity packing. Bytes are copied
	// untouched. DirEntry fields SignedName and so on are signed.
	PlainPack Packing = 1

	// Packings from 2 through 19 are not for production use. This region
	// is reserved for debugging and other temporary packing implementations.

	// Packings from 20 and above (as well as PlainPack) are fixed in
	// value and semantics and may be used in production.

	// EEPack provides elliptic-curve end-to-end confidentiality and
	// integrity protection. It stores AES-encrypted data, with metadata
	// including an ECDSA signature and ECDH-wrapped keys.
	// (NIST SP 800-57 Pt.1 Rev.4 section 5.6.1)
	// A signature and a per-file symmetric encryption key, wrapped
	// for each reader, are encoded in Packdata.
	// User keys specify a curve:
	// "p256": AES-256, SHA-256, and curve P256; strength 128.
	// "p384": AES-256, SHA-512, and curve P384; strength 192.
	// "p521": AES-256, SHA-512, and curve P521; strength 256.
	// TODO(ehg) add "25519":  x/crypto/curve25519, github.com/agl/ed25519
	EEPack Packing = 20

	// EEIntegrityPack provides elliptic-curve end-to-end integrity protection,
	// like EEPack, but provides no confidentiality.
	// It is typically used when read access is "all".
	EEIntegrityPack Packing = 22
)

// User represents all the public information about an Upspin user as returned by KeyServer.
type User struct {
	// Name represents the user's name as an e-mail address, such as ann@example.com.
	Name UserName

	// Dirs is a slice of DirServer endpoints where the user's root directory may be located,
	// which should be contacted in order when trying to access the user's tree. Multiple
	// entries represent mirrors, which are as yet unimplemented. TODO.
	Dirs []Endpoint

	// Stores is a slice of StoreServer endpoints where the user's data is primarily written to,
	// which should be contacted in order when trying to access the user's data. Multiple
	// entries represent mirrors, which are as yet unimplemented. TODO.
	Stores []Endpoint

	// PublicKey is the user's current public key.
	PublicKey PublicKey
}

// The KeyServer interface provides access to public information about users.
type KeyServer interface {
	Dialer
	Service

	// Lookup returns all public information about a user.
	Lookup(userName UserName) (*User, error)

	// Put sets or updates information about a user. The user's name must
	// match the authenticated user. The call can update any field except
	// the user name.
	// To add new users, see the signup subcommand of cmd/upspin.
	Put(user *User) error
}

// A PublicKey can be seen by anyone and is used for authenticating a user.
type PublicKey string

// AllUsersKey is a sentinel PublicKey value used to indicate that a
// Packer.Share operation should make the data readable to anyone.
var AllUsersKey = PublicKey("read: all")

var (
	// ErrFollowLink indicates that all or part of a path name has
	// evaluated to a DirEntry that is a link. In that case, the returned
	// DirEntry will be that of the link, and its Name field is guaranteed
	// to be an element-wise prefix of the argument path name. The caller
	// should retry the operation, substituting that prefix (which may be
	// the entire name) with the contents of the Link field of the returned
	// DirEntry.
	ErrFollowLink = errors.New("action incomplete: must follow link")

	// ErrNotSupported indicates that the server does not support the
	// requested operation.
	ErrNotSupported = errors.New("not supported")
)

// MaxLinkHops is the maximum number of links that will be followed
// when evaluating a single path name.
const MaxLinkHops = 20

// Special Sequence values for Watch that can be used in place of the
// Sequence argument in the Watch function.
const (
	// WatchStart returns all known events.
	WatchStart = -iota
	// WatchCurrent first sends a sequence of events describing the entire
	// tree rooted at name. The Events are sent in sequence such that a
	// directory is sent before its contents. After the full tree has been
	// sent, the operation proceeds as normal.
	WatchCurrent
	// WatchNew returns only new events.
	WatchNew
)

// DirServer manages the name space for one or more users.
type DirServer interface {
	Dialer
	Service

	// Lookup returns the directory entry for the named file.
	//
	// If the returned error is ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// ErrFollowLink. Otherwise in the case of error the
	// returned DirEntry will be nil.
	Lookup(name PathName) (*DirEntry, error)

	// Put stores the DirEntry in the directory server. The entry
	// may be a plain file, a link, or a directory. (Only one of
	// these attributes may be set.)
	// In practice the data for the file should be stored in
	// a StoreServer as specified by the blocks in the entry,
	// all of which should be stored with the same packing.
	//
	// Within the DirEntry, several fields have special properties.
	// Time represents a timestamp for the item. It is advisory only
	// but is included in the packing signature and so should usually
	// be set to a non-zero value.
	//
	// Sequence represents a sequence number that is incremented
	// after each Put. If it is neither 0 nor -1, the DirServer will
	// reject the Put operation if the file does not exist or, for an
	// existing item, if the Sequence is not the same as that
	// stored in the metadata. If it is -1, Put will fail if there
	// is already an item with that name.
	//
	// The Name field of the DirEntry identifies where in the directory
	// tree the entry belongs. The SignedName field, which usually has the
	// same value, is the name used to sign the DirEntry to guarantee its
	// security. They may differ if an entry appears in multiple locations,
	// such as in its original location plus within a second tree holding
	// a snapshot of the original tree but starting from a different root.
	//
	// Most software will concern itself only with the Name field unless
	// generating or validating the entry's signature.
	//
	// All but the last element of the path name must already exist
	// and be directories or links. The final element, if it exists,
	// must not be a directory. If something is already stored under
	// the path, the new location and packdata replace the old.
	//
	// If the returned error is ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// ErrFollowLink (with the added step of updating the
	// Name field of the argument DirEntry). For any other error,
	// the return DirEntry will be nil.
	//
	// A successful Put returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	Put(entry *DirEntry) (*DirEntry, error)

	// Glob matches the pattern against the file names of the full
	// rooted tree. That is, the pattern must look like a full path
	// name, but elements of the path may contain metacharacters.
	// Matching is done using Go's path.Match elementwise. The user
	// name must be present in the pattern and is treated as a literal
	// even if it contains metacharacters.
	// If the caller has no read permission for the items named in the
	// DirEntries, the returned Location and Packdata fields are cleared.
	//
	// If the returned error is ErrFollowLink, one or more of the
	// returned DirEntries is a link (the others are completely
	// evaluated). The caller should retry the operation for those
	// DirEntries as outlined in the description for ErrFollowLink,
	// updating the pattern as appropriate. Note that any returned
	// links may only partially match the original argument pattern.
	//
	// If the pattern evaluates to one or more name that identifies
	// a link, the DirEntry for the link is returned, not the target.
	// This is analogous to passing false as the second argument
	// to Client.Lookup.
	Glob(pattern string) ([]*DirEntry, error)

	// Delete deletes the DirEntry for a name from the directory service.
	// It does not delete the data it references; use StoreServer.Delete
	// for that. If the name identifies a link, Delete will delete the
	// link itself, not its target.
	//
	// If the returned error is ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// ErrFollowLink. (And in that case, the DirEntry will never
	// represent the full path name of the argument.) Otherwise, the
	// returned DirEntry will be nil whether the operation succeeded
	// or not.
	Delete(name PathName) (*DirEntry, error)

	// WhichAccess returns the DirEntry of the Access file that is
	// responsible for the access rights defined for the named item.
	// WhichAccess requires that the calling user have at least one access
	// right granted for the argument name. If not, WhichAccess will return
	// a "does not exist" error, even if the item and/or the Access file
	// exist.
	//
	// If the returned error is ErrFollowLink, the caller should
	// retry the operation as outlined in the description for
	// ErrFollowLink. Otherwise, in the case of error the returned
	// DirEntry will be nil.
	WhichAccess(name PathName) (*DirEntry, error)

	// Watch returns a channel of Events that describe operations that
	// affect the specified path and any of its descendants, beginning
	// at the specified sequence number for the corresponding user root.
	//
	// If sequence is 0, all events known to the DirServer are sent.
	//
	// If sequence is WatchCurrent, the server first sends a sequence
	// of events describing the entire tree rooted at name. The Events are
	// sent in sequence such that a directory is sent before its contents.
	// After the full tree has been sent, the operation proceeds as normal.
	//
	// If sequence is WatchNew, the server sends only new events.
	//
	// If the sequence is otherwise invalid, this is reported by the
	// server sending a single event with a non-nil Error field with
	// Kind=errors.Invalid. The events channel is then closed.
	//
	// When the provided done channel is closed the event channel
	// is closed by the server.
	//
	// To receive an event for a given path under name, the caller must have
	// one or more of the Upspin access rights to that path. Events for
	// which the caller does not have enough rights to watch will not be
	// sent. If the caller has rights but not Read, the entry will be
	// present but incomplete (see the description of AttrIncomplete). If
	// the name does not exist, Watch will succeed and report events if and
	// when it is created.
	//
	// If the caller does not consume events in a timely fashion
	// the server will close the event channel.
	//
	// If this server does not support this method it returns
	// ErrNotSupported.
	//
	// The only errors returned by the Watch method itself are
	// to report that the name is invalid or refers to a non-existent
	// root, or that the operation is not supported.
	Watch(name PathName, sequence int64, done <-chan struct{}) (<-chan Event, error)
}

// Event represents the creation, modification, or deletion of a DirEntry
// within a DirServer.
type Event struct {
	// Entry is the DirEntry to which the event pertains. Its Sequence
	// field captures the ordering of events for this user.
	Entry *DirEntry

	// Delete is true only if the entry is being deleted;
	// otherwise it is being created or modified.
	Delete bool

	// Error is non-nil if an error occurred while waiting for events.
	// In that case, all other fields are zero.
	Error error
}

// Time represents a timestamp in units of seconds since
// the Unix epoch, Jan 1 1970 0:00 UTC.
type Time int64

// DirEntry represents the directory information for a file.
// The blocks of a file represent contiguous data. There are no
// holes and no overlaps and the first block always has offset 0.
// Name and SignedName must not be empty. See comments in DirServer.Put.
type DirEntry struct {
	// Fields contributing to the signature.
	SignedName PathName   // The full path name of the file used for signing.
	Link       PathName   // The link target, iff the DirEntry has Attr=AttrLink.
	Attr       Attribute  // Attributes for the DirEntry.
	Packing    Packing    // Packing used for every block in file.
	Time       Time       // Time associated with file; might be when it was last written.
	Blocks     []DirBlock // Descriptors for each block. A nil or empty slice represents an empty file.
	Packdata   []byte     // Information maintained by the packing algorithm.

	// Field determining the key used for the signature, hence also tamper-resistant.
	Writer UserName // Writer of the file, often the same as owner.

	// Fields not included in the signature.
	Name     PathName // The full path name of the file. Only the last element can be a link.
	Sequence int64    // The sequence (version) number of the item.
}

// BlockSize is an arbitrarily chosen size that packers use when breaking
// data into blocks for storage. Clients are free to use any size, but this
// value is used in various helper libraries.
// This is also the default value for flags.BlockSize, but must be kept in
// sync manually because the flags package cannot import this package.
const BlockSize = 1024 * 1024

// MaxBlockSize is the maximum size permitted for a block. The limit
// guarantees that 32-bit machines can process the data without problems.
const MaxBlockSize = 1024 * 1024 * 1024

// DirBlock describes a block of data representing a contiguous section of a file.
// The block may be of any non-negative size, but in large files is usually
// BlockSize long.
type DirBlock struct {
	Location Location // Location of data in store.
	Offset   int64    // Byte offset of start of block's data in file.
	Size     int64    // Length of block data in bytes.
	Packdata []byte   // Information maintained by the packing algorithm.
}

// Attribute defines the attributes for a DirEntry.
type Attribute byte

// Supported Attributes.
const (
	// AttrNone is the default attribute, identifying a plain data object.
	AttrNone = Attribute(0)
	// AttrDirectory identifies a directory. It must be the only attribute.
	AttrDirectory = Attribute(1 << 0)
	// AttrLink identifies a link. It must be the only attribute.
	// A link is a path name whose DirEntry identifies another
	// "target" item in the tree, similar to a Unix symbolic link.
	// The target of a link may be another link.
	// The target path is stored in the Link field of the DirEntry.
	// A link DirEntry holds zero DirBlocks.
	AttrLink = Attribute(1 << 1)
	// AttrIncomplete identifies a DirEntry whose Blocks and Packdata
	// fields are elided for access control purposes, or the reply to
	// a successful Put containing only the updated sequence number.
	AttrIncomplete = Attribute(1 << 2)
)

// Sequence numbers.
// Sequence numbers are controlled by the DirServer. For a given user root they
// start at SeqBase and grow monotonically (typically but not necessarily by
// one) with each Put or Delete operation in that user's tree. After an item is
// Put to or Deleted from the DirServer, the Sequence of that item (or its
// directory, for a Delete) and all of the directories on its path will be set
// to the next Sequence number for the user tree. Thus, as a corollary, any
// directory but in particular the user root always has the Sequence number of
// the most recently modified item at that level or deeper in the tree.
//
// When a file or directory is being created, the sequence number in the
// DirEntry provided to Put must be either SeqNotExist or SeqIgnore.
const (
	SeqNotExist = -1 // Put will fail if item exists.
	SeqIgnore   = 0  // Put will not check sequence number, but will update it.
	SeqBase     = 1  // Base at which valid sequence numbers start.
)

// Refdata attaches information about cacheability to a Reference. A Refdata is
// returned by a StoreServer to describe the lifetime of the data associated with
// a Reference.
type Refdata struct {
	Reference Reference     // The reference itself.
	Volatile  bool          // If true, the data might change on every Get and cannot be cached.
	Duration  time.Duration // For non-volatile data, the predicted cacheable lifetime; 0 means forever.
}

// The StoreServer saves and retrieves data without interpretation.
type StoreServer interface {
	Dialer
	Service

	// Get attempts to retrieve the data identified by the reference.
	// Three things might happen:
	// 1. The data is in this StoreServer. It is returned. The Location slice
	// and error are nil. Refdata contains information about the data.
	// 2. The data is not in this StoreServer, but may be in one or more
	// other locations known to the store. The slice of Locations
	// is returned. The data, Refdata, Locations, and error are nil.
	// 3. An error occurs. The data, Locations and Refdata are nil
	// and the error describes the problem.
	Get(ref Reference) ([]byte, *Refdata, []Location, error)

	// Put puts the data into the store and returns the reference
	// to be used to retrieve it.
	Put(data []byte) (*Refdata, error)

	// Delete permanently removes all storage space associated
	// with the reference. After a successful Delete, calls to Get with the
	// same reference will fail. If the reference is not found, an error is
	// returned. Implementations may disable this method except for
	// privileged users.
	Delete(ref Reference) error
}

// Client API.

// The Client interface provides a higher-level API suitable for applications
// that wish to access Upspin's name space. Most Upspin programs should
// use the Client interface to talk to Upspin services.
//
// When Client evaluates a path name and encounters a link, it evaluates the
// link, iteratively if necessary, until it reaches an item that is not a link.
// (The DirServer interface does not evaluate links.)
//
// In methods where a name is evaluated and a DirEntry returned,
// if links were evaluated in processing the operation, the Name field
// of the DirEntry will be different from the argument path name and
// will hold the link-free path to item.
type Client interface {
	// Get returns the clear, decrypted data stored under the given name.
	// It is intended only for special purposes, since it will allocate memory
	// for the entire "blob" to return. The file-like API below can be less
	//  memory-intensive.
	Get(name PathName) ([]byte, error)

	// Lookup returns the directory entry for the named file. The
	// boolean determines whether, if the final path element is a link,
	// to return the DirEntry for the link (false) or for the target of
	// the link (true).
	Lookup(name PathName, followFinal bool) (*DirEntry, error)

	// Put stores the data at the given name. If something is already
	// stored with that name, it will no longer be available using the
	// name, although it may still exist in the storage server. (See
	// the documentation for Delete.) Like Get, it is not the usual
	// access method. The file-like API is preferred.
	//
	// A successful Put returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	Put(name PathName, data []byte) (*DirEntry, error)

	// PutSequenced stores the data at the given name only if
	// there is no preexisting data stored with that name or if the
	// sequence number of the preexisting data matches that given.
	// PutSequenced with SeqIgnore is the same as Put.
	// On success any preexisting data will no longer be available using
	// the name, although it may still exist in the storage server. (See
	// the documentation for Delete.) Like Get, it is not the usual
	// access method. The file-like API is preferred.
	//
	// A successful PutSequenced returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	PutSequenced(name PathName, seq int64, data []byte) (*DirEntry, error)

	// PutLink creates a link from the new name to the old name. The
	// new name must not look like the path to an Access or Group file.
	// If something is already stored with the new name, it is first
	// deleted from the directory but its storage is not deleted from
	// the Store. (See the documentation for Delete.) The old name is
	// not evaluated, that is, the resulting link will hold the
	// argument to PutLink even if it refers to a path that itself
	// contains links. The name is canonicalized, however (see
	// path.Clean).
	//
	// A successful PutLink returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	PutLink(oldName, newName PathName) (*DirEntry, error)

	// PutDuplicate creates a new name for the references referred to
	// by the old name. Subsequent Puts to either name do not effect
	// the contents referred to by the other. There must be no existing
	// item with the new name. If the final element of the path name
	// is a link, PutDuplicate will duplicate the link and not the
	// link target.
	//
	// A successful PutDuplicate returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	PutDuplicate(oldName, newName PathName) (*DirEntry, error)

	// MakeDirectory creates a directory with the given name, which
	// must not already exist. All but the last element of the path
	// name must already exist and be directories.
	//
	// A successful MakeDirectory returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	MakeDirectory(dirName PathName) (*DirEntry, error)

	// Rename renames oldName to newName. The old name is no longer valid.
	// If the final element of the path name is a link, Rename will
	// Rename the link itself, not the link target.
	//
	// A successful Rename returns an incomplete DirEntry (see the
	// description of AttrIncomplete) containing only the
	// new sequence number.
	Rename(oldName, newName PathName) (*DirEntry, error)

	// SetTime sets the time in name's DirEntry. If the final element
	// of the path name is a link, SetTime will affect the link itself,
	// not the link target.
	SetTime(name PathName, t Time) error

	// Delete deletes the DirEntry associated with the name. The
	// storage referenced by the DirEntry is not deleted,
	// although the storage server may garbage collect unreferenced
	// data independently. If the final element of the path name is a
	// link, Delete will delete the link itself, not the link target.
	Delete(name PathName) error

	// Glob matches the pattern against the file names of the full
	// rooted tree. That is, the pattern must look like a full path
	// name, but elements of the path may contain metacharacters.
	// Matching is done using Go's path.Match elementwise. The user
	// name must be present in the pattern and is treated as a literal
	// even if it contains metacharacters. Note that if links are
	// evaluated while executing Glob, the Name fields of the returned
	// DirEntries might not match the original argument pattern.
	Glob(pattern string) ([]*DirEntry, error)

	// Open and Create are file-like methods similar to Go's os.File API.
	// The name, however, is a fully-qualified upspin PathName.
	Create(name PathName) (File, error)
	Open(name PathName) (File, error)

	// DirServer returns an error or a reachable bound DirServer for the user.
	DirServer(name PathName) (DirServer, error)
}

// The File interface has semantics and an API that parallels a subset
// of Go's os.File. The main semantic difference, besides the limited
// method set, is that a Read will only return once the entire contents
// have been decrypted and verified.
type File interface {
	// Close releases the resources. For a writable file, it also
	// writes the accumulated data in a StoreServer. After a
	// Close, successful or not, all methods of File except Name
	// will fail.
	Close() error

	// Name returns the full path name of the File.
	Name() PathName

	// Read, ReadAt, Write, WriteAt and Seek implement
	// the standard Go interfaces io.Reader, etc.
	// Because of the nature of Upspin storage, the entire
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

// Config contains client information such as the user's keys and
// preferred KeyServer, DirServer, and StoreServer endpoints.
type Config interface {
	// The name of the user requesting access.
	UserName() UserName

	// Factotum holds the user's cryptographic keys and encapsulates crypto operations.
	Factotum() Factotum

	// Packing is the Packing to use when creating new data items, although
	// it may be overridden by the implementation, such as to use a cleartext
	// packing for a globally readable ("read:all")  file.
	Packing() Packing

	// KeyEndpoint is the endpoint of the KeyServer to contact to retrieve keys.
	KeyEndpoint() Endpoint

	// DirEndpoint is the endpoint of the DirServer in which to place new data items. It is
	// usually the location of the user's root.
	DirEndpoint() Endpoint

	// StoreEndpoint is the endpoint of the StoreServer in which to place new data items.
	StoreEndpoint() Endpoint

	// CacheEndpoint is the endpoint of the cache server between the client and the StoreServer and DirServers.
	CacheEndpoint() Endpoint

	// Value returns the value for the given configuration key.
	Value(key string) string
}

// Dialer defines how to connect and authenticate to a server. Each
// service type (KeyServer, DirServer, StoreServer) implements the methods of
// the Dialer interface. These methods are not used directly by
// clients. Instead, clients should use the methods of
// the Upspin "bind" package to connect to services.
type Dialer interface {
	// Dial connects to the service and performs any needed authentication.
	Dial(Config, Endpoint) (Service, error)
}

// Service is the general interface returned by a dialer. It includes
// methods to configure the service and report its setup.
type Service interface {
	// Endpoint returns the network endpoint of the server.
	Endpoint() Endpoint

	// Close closes the connection to the service and releases all resources used.
	// A Service may not be re-used after close.
	Close()
}
