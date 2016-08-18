// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unassigned implements a store server that errors out all its requests.
package unassigned

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// unassigned implements upspin.DirServer.
type unassigned struct {
	endpoint upspin.Endpoint
}

var _ upspin.DirServer = (*unassigned)(nil)

var unassignedErr = errors.Str("request to unassigned service")

// Glob implements upspin.DirServer.Glob.
func (*unassigned) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "dir/unassigned.Glob"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// MakeDirectory implements upspin.DirServer.MakeDirectory.
func (*unassigned) MakeDirectory(directoryName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/unassigned.MakeDirectory"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Put implements upspin.DirServer.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (*unassigned) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/unassigned.Put"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (*unassigned) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/unassigned.WhichAccess"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Delete implements upspin.DirServer.Delete.
func (*unassigned) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/unassigned.Delete"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Lookup implements upspin.DirServer.Lookup.
func (*unassigned) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/unassigned.Lookup"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Endpoint implements upspin.Service.
func (u *unassigned) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (*unassigned) Configure(options ...string) (upspin.UserName, error) {
	const op = "dir/unassigned.Configure"
	return "", errors.E(op, errors.Invalid, unassignedErr)
}

// Close implements upspin.Service.
func (*unassigned) Close() {
}

// Authenticate implements upspin.Service.
func (*unassigned) Authenticate(context upspin.Context) error {
	const op = "dir/unassigned.Authenticate"
	return errors.E(op, errors.Invalid, unassignedErr)
}

// Ping implements upspin.Service.
func (*unassigned) Ping() bool {
	return true
}

// Dial implements upspin.Service.
func (u *unassigned) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "dir/unassigned.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}

	return &unassigned{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterDirServer(transport, &unassigned{})
}
