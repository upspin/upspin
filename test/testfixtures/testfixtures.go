// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testfixtures implements dummies for Store, Directory and User services for tests.
package testfixtures

import "upspin.io/upspin"

// DummyUser is an implementation of upspin.User that does nothing.
type DummyUser struct {
	dummyDialer
	dummyService
}

var _ upspin.User = (*DummyUser)(nil)

// DummyStore is an implementation of upspin.Store that does nothing.
type DummyStore struct {
	dummyDialer
	dummyService
}

var _ upspin.Store = (*DummyStore)(nil)

// DummyDirectory is an implementation of upspin.Directory that does nothing.
type DummyDirectory struct {
	dummyDialer
	dummyService
}

var _ upspin.Directory = (*DummyDirectory)(nil)

// dummyService implements a no-op upspin.Service
type dummyService struct {
}

var _ upspin.Service = (*dummyService)(nil)

type dummyDialer struct {
}

var _ upspin.Dialer = (*dummyDialer)(nil)

// Dial implements upspin.Dialer.
func (d *dummyDialer) Dial(upspin.Context, upspin.Endpoint) (upspin.Service, error) {
	return nil, nil
}

// Endpoint implements upspin.Service.
func (d *dummyService) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{}
}

// Configure implements upspin.Service.
func (d *dummyService) Configure(options ...string) error {
	return nil
}

// Authenticate implements upspin.Service.
func (d *dummyService) Authenticate(upspin.Context) error {
	return nil
}

// Close implements upspin.Service.
func (d *dummyService) Close() {
}

// Ping implements upspin.Service.
func (d *dummyService) Ping() bool {
	return true
}

// Lookup implements upspin.User.
func (d *DummyUser) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	return nil, nil, nil
}

// Get implements upspin.Store.
func (d *DummyStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	return nil, nil, nil
}

// Put implements upspin.Store.
func (d *DummyStore) Put(data []byte) (upspin.Reference, error) {
	return "", nil
}

// Delete implements upspin.Store.
func (d *DummyStore) Delete(ref upspin.Reference) error {
	return nil
}

// Lookup implements upspin.Directory.
func (d *DummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}

// Put implements upspin.Directory.
func (d *DummyDirectory) Put(entry *upspin.DirEntry) error {
	return nil
}

// MakeDirectory implements upspin.Directory.
func (d *DummyDirectory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, nil
}

// Glob implements upspin.Directory.
func (d *DummyDirectory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, nil
}

// Delete implements upspin.Directory.
func (d *DummyDirectory) Delete(name upspin.PathName) error {
	return nil
}

// WhichAccess implements upspin.Directory.
func (d *DummyDirectory) WhichAccess(name upspin.PathName) (upspin.PathName, error) {
	return "", nil
}
