// Package teststore implements a simple non-persistent in-memory store service.
package teststore

import (
	"errors"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/key/sha256key"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service returns data and metadata referenced by the request.
type Service struct {
	endpoint upspin.Endpoint
	blob     map[string][]byte // string is key made from SHA256 hash of data.
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
	key := sha256key.Of(ciphertext).String()
	s.blob[key] = ciphertext
	return key, nil
}

func (s *Service) Delete(key string) error {
	return errors.New("Not implemented yet")
}

// TODO: Function should provide alternate location if missing.
func (s *Service) Get(key string) (ciphertext []byte, other []upspin.Location, err error) {
	data, ok := s.blob[key]
	if !ok {
		return nil, nil, errors.New("no such blob")
	}
	if sha256key.Of(data).String() != key {
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
func (s *Service) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
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
