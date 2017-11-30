// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple non-persistent in-memory store service.
package inprocess // import "upspin.io/store/inprocess"

import (
	"sync"

	"upspin.io/errors"
	"upspin.io/key/sha256key"
	"upspin.io/upspin"
)

// service returns data and metadata referenced by the request.
// There is one for each Dial call.
type service struct {
	data *dataService
}

var _ upspin.StoreServer = (*service)(nil)

func New() upspin.StoreServer {
	return &service{
		data: &dataService{
			endpoint: upspin.Endpoint{
				Transport: upspin.InProcess,
				NetAddr:   "", // Ignored.
			},
			blob: make(map[upspin.Reference][]byte),
		},
	}
}

// A dataService is the underlying service object.
// There is one for the entire system, created in init.
type dataService struct {
	endpoint upspin.Endpoint
	// mu protects the fields of dataService.
	mu sync.Mutex
	// dialed reports whether anyone has dialed us.
	dialed bool
	// blob contains the underlying data.
	blob map[upspin.Reference][]byte // reference is made from SHA256 hash of data.
}

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

// Endpoint implements upspin.StoreServer
func (s *service) Endpoint() upspin.Endpoint {
	return s.data.endpoint
}

// Put implements upspin.StoreServer
func (s *service) Put(ciphertext []byte) (*upspin.Refdata, error) {
	ref := upspin.Reference(sha256key.Of(ciphertext).String())
	s.data.mu.Lock()
	s.data.blob[ref] = copyOf(ciphertext)
	s.data.mu.Unlock()
	refdata := &upspin.Refdata{
		Reference: ref,
		Volatile:  false,
		Duration:  0,
	}
	return refdata, nil
}

// Delete implements upspin.StoreServer
func (s *service) Delete(ref upspin.Reference) error {
	const op errors.Op = "store/inprocess.Delete"
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	_, ok := s.data.blob[ref]
	if !ok {
		return errors.E(op, errors.NotExist, errors.Errorf("no such blob: %s", ref))
	}
	delete(s.data.blob, ref)
	return nil
}

// DeleteAll deletes all data from memory.
func (s *service) DeleteAll() {
	s.data.mu.Lock()
	s.data.blob = make(map[upspin.Reference][]byte)
	s.data.mu.Unlock()
}

// Get implements upspin.StoreServer
// TODO: Get should provide alternate location if missing.
func (s *service) Get(ref upspin.Reference) (ciphertext []byte, refdata *upspin.Refdata, other []upspin.Location, err error) {
	const op errors.Op = "store/inprocess.Get"
	if ref == "" {
		return nil, nil, nil, errors.E(op, errors.Invalid, "empty reference")
	}
	s.data.mu.Lock()
	data, ok := s.data.blob[ref]
	s.data.mu.Unlock()
	if !ok {
		return nil, nil, nil, errors.E(op, errors.NotExist, errors.Errorf("no such blob: %s", ref))
	}
	if upspin.Reference(sha256key.Of(data).String()) != ref {
		return nil, nil, nil, errors.E(op, errors.Invalid, "internal hash mismatch in StoreServer.Get")
	}
	refdata = &upspin.Refdata{
		Reference: ref,
		Volatile:  false,
		Duration:  0,
	}
	return copyOf(data), refdata, nil, nil
}

// Dial always returns an authenticated instance to the underlying service.
// There is only one data set in the address space.
// Dial ignores the address within the endpoint but requires that the transport be InProcess.
// TODO: Authenticate the caller.
func (s *service) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "store/inprocess.Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	if !s.data.dialed {
		// This is the first call; set the endpoint.
		s.data.endpoint = e
	}
	thisStore := *s // Make a copy.
	return &thisStore, nil
}

// Close implements upspin.Service.
func (s *service) Close() {
	// TODO
}
