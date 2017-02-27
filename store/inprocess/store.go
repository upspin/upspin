// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a simple non-persistent in-memory store service.
package inprocess // import "upspin.io/store/inprocess"

import (
	"strconv"
	"strings"
	"sync"

	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/upspin"
)

// service returns data and metadata referenced by the request.
// There is one for each Dial call.
type service struct {
	data *dataService
}

var _ upspin.StoreServer = (*service)(nil)

const maxInt = int(^uint(0) >> 1)

var errBlockTooLarge = errors.E(errors.IO, errors.Str("block too large"))

func New(options ...string) (upspin.StoreServer, error) {
	const op = "store/inprocess.New"
	capacity := int64(100 * 1024 * 1024) // 100 MB by default.
	var err error
	for _, optPair := range options {
		opt := strings.Split(optPair, "=")
		if len(opt) != 2 {
			return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid option format: %q", optPair))
		}
		k, v := opt[0], opt[1]
		switch k {
		case "capacity":
			capacity, err = strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid capacity %q: %s", v, err))
			}
		}
	}
	return &service{
		data: &dataService{
			endpoint: upspin.Endpoint{
				Transport: upspin.InProcess,
				NetAddr:   "", // Ignored.
			},
			blob:     cache.NewLRU(maxInt),
			capacity: capacity,
		},
	}, nil
}

type blob struct {
	dataService *dataService
	data        []byte
}

// A dataService is the underlying service object.
// There is one for the entire system, created in init.
type dataService struct {
	endpoint upspin.Endpoint
	// capacity is the maximum number of bytes this service can store.
	capacity int64
	// mu protects the fields of dataService.
	mu sync.Mutex
	// dialed reports whether anyone has dialed us.
	dialed bool
	// blob contains the underlying data for a reference.
	// key is a reference, made from SHA256 hash of data.
	// value is the data, as type blob.
	blob *cache.LRU
	// usage is how much this service is currently storing.
	usage int64
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
	const op = "store/inprocess.Put"
	ref := upspin.Reference(sha256key.Of(ciphertext).String())

	putSize := int64(len(ciphertext))
	// Can a single put be larger than our entire capacity?
	if putSize > s.data.capacity {
		return nil, errors.E(op, errBlockTooLarge)
	}

	s.data.mu.Lock()
	s.data.usage += putSize
	b := &blob{
		data:        copyOf(ciphertext),
		dataService: s.data,
	}
	// Add must be in the critical section because OnEviction can be called.
	s.data.blob.Add(ref, b)
	s.data.maybeFreeSpace()
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
	const op = "store/inprocess.Delete"
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	data := s.data.blob.Remove(ref)
	if data == nil {
		return errors.E(op, errors.NotExist, errors.Errorf("no such blob: %s", ref))
	}
	s.data.usage -= int64(len(data.([]byte)))
	return nil
}

// DeleteAll deletes all data from memory.
func (s *service) DeleteAll() {
	s.data.mu.Lock()
	s.data.blob = cache.NewLRU(maxInt)
	s.data.usage = 0
	s.data.mu.Unlock()
}

// Get implements upspin.StoreServer
// TODO: Get should provide alternate location if missing.
func (s *service) Get(ref upspin.Reference) (ciphertext []byte, refdata *upspin.Refdata, other []upspin.Location, err error) {
	const op = "store/inprocess.Get"
	if ref == "" {
		return nil, nil, nil, errors.E(op, errors.Invalid, errors.Str("empty reference"))
	}
	s.data.mu.Lock()
	dataBlob, ok := s.data.blob.Get(ref)
	s.data.mu.Unlock()
	if !ok {
		return nil, nil, nil, errors.E(op, errors.NotExist, errors.Errorf("no such blob: %s", ref))
	}
	data := dataBlob.(*blob).data
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
	const op = "store/inprocess.Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
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

// Ping implements upspin.Service.
func (s *service) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *service) Close() {
	// TODO
}

// maybeFreeSpace ensures we don't permanently store more than the capacity.
// It frees space until capacity is back to at most full.
// d.mu must be held.
func (d *dataService) maybeFreeSpace() {
	for {
		if d.usage <= d.capacity {
			return
		}
		key, dataBlob := d.blob.RemoveOldest()
		if dataBlob == nil {
			log.Error.Printf("Did not expect a nil here.")
			continue
		}
		// RemoveOldest does not run OnEviction, so we run it ourselves.
		dataBlob.(*blob).OnEviction(key)
	}
}

// OnEviction implements cache.EvictionNotifier.
// It is always called when the b.dataService.mu is held.
func (b *blob) OnEviction(key interface{}) {
	b.dataService.usage -= int64(len(b.data))
}
