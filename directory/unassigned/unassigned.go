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

// unassigned implements upspin.Directory.
type unassigned struct {
	endpoint upspin.Endpoint
}

var _ upspin.Directory = (*unassigned)(nil)

var unassignedErr = errors.Str("request to unassigned service")

// Glob implements upspin.Directory.Glob.
func (*unassigned) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errors.E("Glob", errors.Invalid, unassignedErr)
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (*unassigned) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, errors.E("MakeDirectory", errors.Invalid, unassignedErr)
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (*unassigned) Put(entry *upspin.DirEntry) error {
	return errors.E("Put", errors.Invalid, unassignedErr)
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (*unassigned) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	return "", errors.E("WhichAccess", errors.Invalid, unassignedErr)
}

// Delete implements upspin.Directory.Delete.
func (*unassigned) Delete(pathName upspin.PathName) error {
	return errors.E("Delete", errors.Invalid, unassignedErr)
}

// Lookup implements upspin.Directory.Lookup.
func (*unassigned) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.E("Lookup", errors.Invalid, unassignedErr)
}

// Endpoint implements upspin.Service.
func (u *unassigned) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (*unassigned) Configure(options ...string) error {
	return errors.E("Configure", errors.Invalid, unassignedErr)
}

// Close implements upspin.Service.
func (*unassigned) Close() {
}

// Authenticate implements upspin.Service.
func (*unassigned) Authenticate(context *upspin.Context) error {
	return errors.E("Authenticate", errors.Invalid, unassignedErr)
}

// Ping implements upspin.Service.
func (*unassigned) Ping() bool {
	return true
}

// Dial implements upspin.Service.
func (u *unassigned) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Unassigned {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}

	return &unassigned{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterDirectory(transport, &unassigned{})
}
