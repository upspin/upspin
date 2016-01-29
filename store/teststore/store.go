// Package teststore implements a simple non-persistent in-memory store service.
package teststore

import (
	"encoding/binary"
	"errors"
	"fmt"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/sim/hash"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Blobs. TODO: Move to a test client package once one is created.
// Message is {N, path[N], data}. N is unsigned varint-encoded.

func MakeBlob(path string, payload []byte) []byte {
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

// UnpackBlob decrypts the data in place and returns the path name and data.
func UnpackBlob(data []byte) (upspin.PathName, []byte, error) {
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
	return upspin.PathName(name), payload, nil
}

// Service returns data and metadata referenced by the request.
type Service struct {
	netAddr upspin.NetAddr
	blob    map[string]*Blob // Key created by blobKey.
}

// This package (well, the Servie type) implements the upspin.Store interface.
var _ upspin.Store = (*Service)(nil)

func blobKey(ref *upspin.Reference) string {
	return fmt.Sprintf("%d:%s", ref.Packing, ref.Key)
}

func NewService(addr upspin.NetAddr) *Service {
	return &Service{
		netAddr: addr,
		blob:    make(map[string]*Blob),
	}
}

type Blob struct {
	data     []byte
	hash     hash.Hash
	metadata []byte // Not sure what this looks like; includes keys, owner, ???
}

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

func (s *Service) NetAddr() upspin.NetAddr {
	return s.netAddr
}

func (s *Service) Put(ref upspin.Reference, ciphertext []byte) (upspin.Location, error) {
	if ref.Packing != upspin.Debug { // TODO
		return upspin.Location{}, errors.New("unrecognized packing")
	}
	hash := hash.Of(ciphertext)
	if !hash.EqualString(ref.Key) {
		return upspin.Location{}, errors.New("external hash mismatch in Store.Put")
	}
	s.blob[blobKey(&ref)] = &Blob{
		copyOf(ciphertext),
		hash,
		[]byte("metadata"), // TODO: probably want defaults.
	}
	loc := upspin.Location{
		NetAddr:   s.netAddr,
		Reference: ref,
	}
	return loc, nil
}

// TODO: Function should provide alternate location if missing.
func (s *Service) Get(loc upspin.Location) (ciphertext []byte, other []upspin.Location, err error) {
	if loc.Reference.Packing != upspin.Debug { // TODO
		return nil, nil, errors.New("unrecognized packing")
	}
	blob, ok := s.blob[blobKey(&loc.Reference)]
	if !ok {
		return nil, nil, errors.New("no such blob")
	}
	if hash.Of(blob.data) != blob.hash {
		return nil, nil, errors.New("internal hash mismatch in Store.Get")
	}
	if !blob.hash.EqualString(loc.Reference.Key) {
		return nil, nil, errors.New("external hash mismatch in Store.Get")
	}
	return copyOf(blob.data), nil, nil
}

// Methods to implement upspin.Access

func (s *Service) ServerUserName() string {
	return "testuser"
}

func (s *Service) Dial(context upspin.ClientContext, loc upspin.Location) (interface{}, error) {
	return NewService(loc.NetAddr), nil
}

func init() {
	service := &Service{
		blob: make(map[string]*Blob),
	}
	access.Switch.RegisterStore("teststore", service)
}
