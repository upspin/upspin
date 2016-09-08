// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package servermux provides in-process KeyServer,
// StoreServer, and DirServer implementations that mux across
// multiple concrete instances/implementations.
// They are muxed by the NetAddr in the Endpoint,
// which can be an arbitrary string.
package servermux

import (
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"

	dirserver "upspin.io/dir/unassigned"
	keyserver "upspin.io/key/unassigned"
	storeserver "upspin.io/store/unassigned"
)

// NewKey creates a new muxing KeyServer and returns
// the corresponding Mux and KeyServer instances.
func NewKey() (*Mux, upspin.KeyServer) {
	mux := &Mux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	key := &key{
		mux,
		keyserver.Server{},
	}
	return mux, key
}

// NewStore creates a new muxing StoreServer and returns
// the corresponding Mux and StoreServer instances.
func NewStore() (*Mux, upspin.StoreServer) {
	mux := &Mux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	store := &store{
		mux,
		storeserver.Server{},
	}
	return mux, store
}

// NewDir creates a new muxing DirServer and returns
// the corresponding Mux and DirServer instances.
func NewDir() (*Mux, upspin.DirServer) {
	mux := &Mux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	dir := &dir{
		mux,
		dirserver.Server{},
	}
	return mux, dir
}

// Mux stores a mapping of upspin.Dialers
// keyed by their Endpoints' NetAddr fields.
type Mux struct {
	mu sync.Mutex
	m  map[upspin.NetAddr]upspin.Dialer
}

// Register adds the given dialer to the Mux's mapping
// keyed by the given Endpoint's NetAddr.
func (mux *Mux) Register(ep upspin.Endpoint, d upspin.Dialer) {
	if ep.Transport != upspin.InProcess {
		panic(fmt.Sprintf("Register with transport %v, want inprocess", ep.Transport))
	}
	mux.mu.Lock()
	defer mux.mu.Unlock()
	if _, ok := mux.m[ep.NetAddr]; ok {
		panic(fmt.Sprintf("Register of already present NetAddr %q", ep.NetAddr))
	}
	mux.m[ep.NetAddr] = d
}

func (mux *Mux) dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	if ep.Transport != upspin.InProcess {
		return nil, errors.Errorf("Dial with transport %v, want inprocess", ep.Transport)
	}
	mux.mu.Lock()
	svc := mux.m[ep.NetAddr]
	mux.mu.Unlock()
	if svc == nil {
		return nil, errors.Errorf("Dial did not recognize NetAddr %q", ep.NetAddr)
	}
	return svc.Dial(ctx, ep)
}

type key struct {
	mux *Mux
	upspin.KeyServer
}

func (s key) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}

type store struct {
	mux *Mux
	upspin.StoreServer
}

func (s store) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}

type dir struct {
	mux *Mux
	upspin.DirServer
}

func (s dir) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}
