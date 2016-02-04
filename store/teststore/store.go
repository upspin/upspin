// Package teststore implements a simple non-persistent in-memory store service.
package teststore

import (
	"encoding/binary"
	"errors"

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
	endpoint upspin.Endpoint
	blob     map[string][]byte // string is key made from SHA1 hash of data.
}

// This package (well, the Servie type) implements the upspin.Store interface.
var _ upspin.Store = (*Service)(nil)

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

func (s *Service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func (s *Service) Put(ciphertext []byte) (string, error) {
	key := hash.Of(ciphertext).String()
	s.blob[key] = ciphertext
	return key, nil
}

// TODO: Function should provide alternate location if missing.
func (s *Service) Get(key string) (ciphertext []byte, other []upspin.Location, err error) {
	data, ok := s.blob[key]
	if !ok {
		return nil, nil, errors.New("no such blob")
	}
	if hash.Of(data).String() != key {
		return nil, nil, errors.New("internal hash mismatch in Store.Get")
	}
	return copyOf(data), nil, nil
}

// Methods to implement upspin.Access

func (s *Service) ServerUserName() string {
	return "testuser"
}

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (s *Service) Dial(context upspin.ClientContext, e upspin.Endpoint) (interface{}, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("teststore: unrecognized transport")
	}
	return s, nil
}

const transport = upspin.InProcess

func init() {
	s := &Service{
		endpoint: upspin.Endpoint{
			Transport: upspin.InProcess,
			NetAddr:   "", // Ignored.
		},
		blob: make(map[string][]byte),
	}
	access.RegisterStore(transport, s)
}
