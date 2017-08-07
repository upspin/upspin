// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testfixtures implements dummies for StoreServers, DirServers and KeyServers for tests.
package testfixtures

import "upspin.io/upspin"

// DummyKey is an implementation of upspin.KeyServer that does nothing.
type DummyKey struct {
	dummyDialer
	dummyService
}

var _ upspin.KeyServer = (*DummyKey)(nil)

// DummyStoreServer is an implementation of upspin.StoreServer that does nothing.
type DummyStoreServer struct {
	dummyDialer
	dummyService
}

var _ upspin.StoreServer = (*DummyStoreServer)(nil)

// DummyDirServer is an implementation of upspin.DirServer that does nothing.
type DummyDirServer struct {
	dummyDialer
	dummyService
}

var _ upspin.DirServer = (*DummyDirServer)(nil)

// dummyService implements a no-op upspin.Service
type dummyService struct {
}

var _ upspin.Service = (*dummyService)(nil)

type dummyDialer struct {
}

var _ upspin.Dialer = (*dummyDialer)(nil)

// Dial implements upspin.Dialer.
func (d *dummyDialer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return nil, nil
}

// Endpoint implements upspin.Service.
func (d *dummyService) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{}
}

// Close implements upspin.Service.
func (d *dummyService) Close() {
}

// Lookup implements upspin.KeyServer.
func (d *DummyKey) Lookup(userName upspin.UserName) (*upspin.User, error) {
	return nil, nil
}

// Put implements upspin.KeyServer.
func (d *DummyKey) Put(user *upspin.User) error {
	return nil
}

// Get implements upspin.StoreServer.
func (d *DummyStoreServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	return nil, nil, nil, nil
}

// Put implements upspin.StoreServer.
func (d *DummyStoreServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, nil
}

// Delete implements upspin.StoreServer.
func (d *DummyStoreServer) Delete(ref upspin.Reference) error {
	return nil
}

// Lookup implements upspin.DirServer.
func (d *DummyDirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}

// Put implements upspin.DirServer.
func (d *DummyDirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, nil
}

// Glob implements upspin.DirServer.
func (d *DummyDirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, nil
}

// Delete implements upspin.DirServer.
func (d *DummyDirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}

// WhichAccess implements upspin.DirServer.
func (d *DummyDirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}

// Watch implements upspin.DirServer.
func (d *DummyDirServer) Watch(upspin.PathName, int64, <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}
