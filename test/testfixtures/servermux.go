// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfixtures

import (
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"

	dirserver "upspin.io/dir/unassigned"
	keyserver "upspin.io/key/unassigned"
	storeserver "upspin.io/store/unassigned"
)

func NewKeyServerMux() (*DialMux, upspin.KeyServer) {
	mux := &DialMux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	key := &keyServerMux{
		mux,
		keyserver.Server{},
	}
	return mux, key
}

func NewStoreServerMux() (*DialMux, upspin.StoreServer) {
	mux := &DialMux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	store := &storeServerMux{
		mux,
		storeserver.Server{},
	}
	return mux, store
}

func NewDirServerMux() (*DialMux, upspin.DirServer) {
	mux := &DialMux{m: make(map[upspin.NetAddr]upspin.Dialer)}
	dir := &dirServerMux{
		mux,
		dirserver.Server{},
	}
	return mux, dir
}

type DialMux struct {
	mu sync.Mutex
	m  map[upspin.NetAddr]upspin.Dialer
}

func (mux *DialMux) Register(ep upspin.Endpoint, d upspin.Dialer) {
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

func (mux *DialMux) dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
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

type keyServerMux struct {
	mux *DialMux
	upspin.KeyServer
}

func (s keyServerMux) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}

type storeServerMux struct {
	mux *DialMux
	upspin.StoreServer
}

func (s storeServerMux) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}

type dirServerMux struct {
	mux *DialMux
	upspin.DirServer
}

func (s dirServerMux) Dial(ctx upspin.Context, ep upspin.Endpoint) (upspin.Service, error) {
	return s.mux.dial(ctx, ep)
}
