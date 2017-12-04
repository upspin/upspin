// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements upspin.StoreServer using storage.Storage as its
// storage back end.
package server // import "upspin.io/store/server"

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
)

// server implements upspin.StoreServer.
type server struct {
	storage storage.Storage

	mu       sync.RWMutex // Protects fields below.
	refCount uint64       // How many clones of us exist.
	linkBase []byte
}

var _ upspin.StoreServer = (*server)(nil)

// New returns a StoreServer that serves the given endpoint with the provided options.
func New(options ...string) (upspin.StoreServer, error) {
	const op errors.Op = "store/server.New"

	var backend string
	var dialOpts []storage.DialOpts
	for _, option := range options {
		const prefix = "backend="
		if strings.HasPrefix(option, prefix) {
			backend = option[len(prefix):]
			continue
		}
		// Pass other options to the storage backend.
		dialOpts = append(dialOpts, storage.WithOptions(option))
	}
	if backend == "" {
		return nil, errors.E(op, errors.Invalid, `storage "backend" option is missing`)
	}
	s, err := storage.Dial(backend, dialOpts...)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return &server{
		storage: s,
	}, nil
}

// Put implements upspin.StoreServer.
func (s *server) Put(data []byte) (*upspin.Refdata, error) {
	const op errors.Op = "store/server.Put"

	m, sp := metric.NewSpan(op)
	sp.SetAnnotation(fmt.Sprintf("size=%d", len(data)))
	defer m.Done()
	defer sp.End()

	ref := sha256key.Of(data).String()
	if err := s.storage.Put(ref, data); err != nil {
		return nil, errors.E(op, err)
	}

	refdata := &upspin.Refdata{
		Reference: upspin.Reference(ref),
		Volatile:  false,
		Duration:  0,
	}
	return refdata, nil
}

// Get implements upspin.StoreServer.
func (s *server) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op errors.Op = "store/server.Get"

	m, sp := metric.NewSpan(op)
	defer m.Done()
	defer sp.End()

	switch {
	case ref == upspin.HTTPBaseMetadata:
		refData := &upspin.Refdata{Reference: ref}
		s.mu.Lock()
		base := s.linkBase
		s.mu.Unlock()
		if base != nil {
			return base, refData, nil, nil
		}
		baseStr, err := s.storage.LinkBase()
		if err == upspin.ErrNotSupported {
			return nil, nil, nil, errors.E(op, errors.NotExist)
		} else if err != nil {
			return nil, nil, nil, errors.E(op, err)
		}
		base = []byte(baseStr)
		s.mu.Lock()
		s.linkBase = base
		s.mu.Unlock()
		return base, refData, nil, nil

	case strings.HasPrefix(string(ref), string(upspin.ListRefsMetadata)):
		ls, ok := s.storage.(storage.Lister)
		if !ok {
			return nil, nil, nil, upspin.ErrNotSupported
		}
		token := strings.TrimPrefix(string(ref), string(upspin.ListRefsMetadata))
		refs, next, err := ls.List(token)
		if err != nil {
			return nil, nil, nil, errors.E(op, err)
		}
		result := upspin.ListRefsResponse{
			Refs: refs,
			Next: next,
		}
		b, err := json.Marshal(result)
		if err != nil {
			return nil, nil, nil, errors.E(op, err)
		}
		refdata := &upspin.Refdata{
			Reference: ref,
			Volatile:  true,
		}
		return b, refdata, nil, nil

	default:
		data, err := s.storage.Download(string(ref))
		if err != nil {
			return nil, nil, nil, errors.E(op, err)
		}
		refdata := &upspin.Refdata{
			Reference: ref,
			Volatile:  false,
			Duration:  0,
		}
		sp.SetAnnotation(fmt.Sprintf("refsize=%d", len(ref)))
		return data, refdata, nil, nil
	}
}

// Delete implements upspin.StoreServer.
func (s *server) Delete(ref upspin.Reference) error {
	const op errors.Op = "store/server.Delete"

	m, _ := metric.NewSpan(op)
	defer m.Done()

	err := s.storage.Delete(string(ref))
	if err != nil {
		return errors.E(op, errors.Errorf("%s: %s", ref, err))
	}
	return nil
}

// Dial implements upspin.Service.
func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refCount++
	return s, nil
}

// Close implements upspin.Service.
func (s *server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refCount == 0 {
		log.Error.Printf("store/server: closing store that was not dialed")
		return
	}
	s.refCount--

	if s.refCount == 0 {
		s.storage = nil
	}
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
