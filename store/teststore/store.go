// Package teststore implements a simple non-persistent in-memory store service.
package teststore

import (
	"errors"
	"sync"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/key/sha256key"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service returns data and metadata referenced by the request.
type Service struct {
	// mu protects the fields below.
	mu       sync.Mutex
	endpoint upspin.Endpoint
	blob     map[upspin.Reference][]byte // reference is made from SHA256 hash of data.
}

// This package (well, the Servie type) implements the upspin.Store interface.
var _ upspin.Store = (*Service)(nil)

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

// Endpoint implements upspin.Store
func (s *Service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

// Put implements upspin.Store
func (s *Service) Put(ciphertext []byte) (upspin.Reference, error) {
	ref := upspin.Reference(sha256key.Of(ciphertext).String())
	s.mu.Lock()
	s.blob[ref] = ciphertext
	s.mu.Unlock()
	return ref, nil
}

// Delete implements upspin.Store
func (s *Service) Delete(upspin.Reference) error {
	return errors.New("Not implemented yet")
}

// Get implements upspin.Store
// TODO: Get should provide alternate location if missing.
func (s *Service) Get(ref upspin.Reference) (ciphertext []byte, other []upspin.Location, err error) {
	if ref == "" {
		return nil, nil, errors.New("empty reference")
	}
	s.mu.Lock()
	data, ok := s.blob[ref]
	s.mu.Unlock()
	if !ok {
		return nil, nil, errors.New("no such blob")
	}
	if upspin.Reference(sha256key.Of(data).String()) != ref {
		return nil, nil, errors.New("internal hash mismatch in Store.Get")
	}
	return copyOf(data), nil, nil
}

// Methods to implement upspin.Dialer

// ServerUserName implements upspin.Dialer
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
		blob: make(map[upspin.Reference][]byte),
	}
	bind.RegisterStore(transport, s)
}
