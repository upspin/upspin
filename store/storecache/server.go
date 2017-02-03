// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storecache is a caching proxy between a client and all stores.
// References are stored as files in the local file system.
package storecache

import (
	"fmt"
	"path"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

// server implements upspin.Storeserver.
type server struct {
	cfg upspin.Config

	// The on disk cache.
	cache *storeCache

	// The store server this dialed server should talk to.
	authority upspin.Endpoint
}

// New creates a new store cache that implements upspin.StoreServer.
func New(cfg upspin.Config, cacheDir string, maxBytes int64, writeBack bool) (upspin.StoreServer, error) {
	c, err := newCache(cfg, path.Join(cacheDir, "storecache"), maxBytes, writeBack)
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:   cfg,
		cache: c,
	}, nil
}

func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s2 := *s
	s2.authority = e
	return &s2, nil
}

var errNotDialed = errors.Str("store/cache: can't handle request to unassigned authority (must dial first)")

func (s *server) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if s.authority.Transport == upspin.Unassigned {
		return nil, nil, nil, errNotDialed
	}

	op := logf("Get %q", ref)

	data, locs, err := s.cache.get(s.cfg, ref, s.authority)
	if err != nil {
		return nil, nil, nil, op.error(err)
	}
	refdata := &upspin.Refdata{
		Reference: ref,
		Volatile:  false, // TODO
		Duration:  0,     // TODO
	}
	return data, refdata, locs, nil
}

func (s *server) Put(data []byte) (*upspin.Refdata, error) {
	if s.authority.Transport == upspin.Unassigned {
		return nil, errNotDialed
	}

	op := logf("Put %.30x...", data)

	ref, err := s.cache.put(s.cfg, data, s.authority)
	if err != nil {
		return nil, op.error(err)
	}
	refdata := &upspin.Refdata{
		Reference: ref,
		Volatile:  false, // TODO
		Duration:  0,     // TODO
	}
	return refdata, nil
}

// Delete implements proto.StoreServer.
func (s *server) Delete(ref upspin.Reference) error {
	if s.authority.Transport == upspin.Unassigned {
		return errNotDialed
	}
	op := logf("Delete %q", ref)

	err := s.cache.delete(s.cfg, ref, s.authority)
	if err != nil {
		return op.error(err)
	}
	return nil
}

func (s *server) Endpoint() upspin.Endpoint { return s.authority }
func (s *server) Close()                    {}
func (s *server) Ping() bool                { return true }

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Debug.Print("store/storecache: " + s)
	return operation(s)
}

type operation string

func (op operation) error(err error) error {
	logf("%v failed: %v", op, err)
	return errors.E("store/storecache."+string(op), err)
}
