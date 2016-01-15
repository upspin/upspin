// Package store implements the store service for the simulator.
package store

import (
	"encoding/binary"
	"errors"

	"upspin.googlesource.com/upspin.git/sim/hash"
	"upspin.googlesource.com/upspin.git/sim/path"
	"upspin.googlesource.com/upspin.git/sim/ref"
)

// Blobs. TODO: Belongs in another package?
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
func UnpackBlob(data []byte) (path.Name, []byte, error) {
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
	return path.Name(name), payload, nil
}

// Service returns data and metadata referenced by the request.
type Service struct {
	Location ref.Location
	blob     map[ref.Reference]*Blob
}

func NewService(loc ref.Location) *Service {
	return &Service{
		Location: loc,
		blob:     make(map[ref.Reference]*Blob),
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

// TODO: Should it return a HintedReference?
func (s *Service) Put(ciphertext []byte) (r ref.Reference, err error) {
	hash := hash.Of(ciphertext)
	r = ref.Reference{
		Hash: hash,
	}
	s.blob[r] = &Blob{
		copyOf(ciphertext),
		hash,
		[]byte("metadata"), // TODO: probably want defaults.
	}
	return r, nil
}

// TODO: API should provide alternate location if missing.
func (s *Service) Get(ref ref.Reference) (ciphertext []byte, err error) {
	blob, ok := s.blob[ref]
	if !ok {
		return nil, errors.New("no such blob")
	}
	if hash.Of(blob.data) != blob.hash {
		return nil, errors.New("internal hash mismatch in StorageService.Get")
	}
	if ref.Hash != blob.hash {
		return nil, errors.New("external hash mismatch in StorageService.Get")
	}
	return copyOf(blob.data), nil
}

func (s *Service) GetMetadata(ref ref.Reference) (cleartext []byte, err error) {
	blob, ok := s.blob[ref]
	if !ok {
		return nil, errors.New("no such blob")
	}
	if hash.Of(blob.data) != blob.hash {
		return nil, errors.New("internal hash mismatch in GetMetadata")
	}
	return copyOf(blob.metadata), nil
}
