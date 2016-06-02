// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple non-persistent in-memory store service.
package inprocess

import (
	"errors"
	"sync"

	"upspin.io/bind"
	"upspin.io/key/sha256key"
	"upspin.io/upspin"
)

// service returns data and metadata referenced by the request.
// There is one for each Dial call.
type service struct {
	upspin.NoConfiguration
	// userName identifies the user accessing the service. TODO: unused.
	userName upspin.UserName
	data     *dataService
}

// A dataService is the underlying service object.
// There is one for the entire system, created in init.
type dataService struct {
	endpoint upspin.Endpoint
	// mu protects the fields of dataService.
	mu sync.Mutex
	// serviceOwner identifies the user running the dataService. TODO: unused.
	serviceOwner upspin.UserName
	// blob contains the underlying data.
	blob map[upspin.Reference][]byte // reference is made from SHA256 hash of data.
}

// This package (well, the service type) implements the upspin.Store interface.
var _ upspin.Store = (*service)(nil)

func copyOf(in []byte) (out []byte) {
	out = make([]byte, len(in))
	copy(out, in)
	return out
}

// Endpoint implements upspin.Store
func (s *service) Endpoint() upspin.Endpoint {
	return s.data.endpoint
}

// Put implements upspin.Store
func (s *service) Put(ciphertext []byte) (upspin.Reference, error) {
	ref := upspin.Reference(sha256key.Of(ciphertext).String())
	s.data.mu.Lock()
	s.data.blob[ref] = ciphertext
	s.data.mu.Unlock()
	return ref, nil
}

// Delete implements upspin.Store
func (s *service) Delete(upspin.Reference) error {
	return errors.New("Not implemented yet")
}

// DeleteAll deletes all data from memory.
func (s *service) DeleteAll() {
	s.data.mu.Lock()
	s.data.blob = make(map[upspin.Reference][]byte)
	s.data.mu.Unlock()
}

// Get implements upspin.Store
// TODO: Get should provide alternate location if missing.
func (s *service) Get(ref upspin.Reference) (ciphertext []byte, other []upspin.Location, err error) {
	if ref == "" {
		return nil, nil, errors.New("empty reference")
	}
	s.data.mu.Lock()
	data, ok := s.data.blob[ref]
	s.data.mu.Unlock()
	if !ok {
		return nil, nil, errors.New("no such blob")
	}
	if upspin.Reference(sha256key.Of(data).String()) != ref {
		return nil, nil, errors.New("internal hash mismatch in Store.Get")
	}
	return copyOf(data), nil, nil
}

// ServerUserName implements upspin.service.
func (s *service) ServerUserName() string {
	return "inprocess"
}

// Dial always returns an authenticated instance to the underlying service.
// There is only one data set in the address space.
// Dial ignores the address within the endpoint but requires that the transport be InProcess.
// TODO: Authenticate the caller.
func (s *service) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("store/inprocess: unrecognized transport")
	}
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	if s.data.serviceOwner == "" {
		// This is the first call; set the owner and endpoint.
		s.data.endpoint = e
		s.data.serviceOwner = context.UserName
	}
	thisUser := *s // Make a copy.
	thisUser.userName = context.UserName
	return &thisUser, nil
}

// Ping implements upspin.Service.
func (s *service) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *service) Close() {
	// TODO
}

// Authenticate implements upspin.Service.
func (s *service) Authenticate(*upspin.Context) error {
	// TODO
	return nil
}

const transport = upspin.InProcess

func init() {
	s := &service{
		data: &dataService{
			endpoint: upspin.Endpoint{
				Transport: upspin.InProcess,
				NetAddr:   "", // Ignored.
			},
			blob: make(map[upspin.Reference][]byte),
		},
	}
	bind.RegisterStore(transport, s)
}
