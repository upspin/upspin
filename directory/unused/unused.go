// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// unused implements upspin.Directory.
type unused struct {
	endpoint upspin.Endpoint
}

var _ upspin.Directory = (*unused)(nil)

var unusedErr = errors.Str("request to 'unused' service")

// Glob implements upspin.Directory.Glob.
func (*unused) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errors.E("Glob", errors.Invalid, unusedErr)
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (*unused) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, errors.E("MakeDirectory", errors.Invalid, unusedErr)
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (*unused) Put(entry *upspin.DirEntry) error {
	return errors.E("Put", errors.Invalid, unusedErr)
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (*unused) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	return "", errors.E("WhichAccess", errors.Invalid, unusedErr)
}

// Delete implements upspin.Directory.Delete.
func (*unused) Delete(pathName upspin.PathName) error {
	return errors.E("Delete", errors.Invalid, unusedErr)
}

// Lookup implements upspin.Directory.Lookup.
func (*unused) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.E("Lookup", errors.Invalid, unusedErr)
}

// Endpoint implements upspin.Service.
func (u *unused) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (*unused) Configure(options ...string) error {
	return errors.E("Configure", errors.Invalid, unusedErr)
}

// Close implements upspin.Service.
func (*unused) Close() {
}

// Authenticate implements upspin.Service.
func (*unused) Authenticate(context *upspin.Context) error {
	return errors.E("Authenticate", errors.Invalid, unusedErr)
}

// Ping implements upspin.Service.
func (*unused) Ping() bool {
	return true
}

// Dial implements upspin.Service.
func (u *unused) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Unused {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}

	return &unused{e}, nil
}

const transport = upspin.Unused

func init() {
	bind.RegisterDirectory(transport, &unused{})
}
