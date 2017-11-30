// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unassigned implements a directory server that errors out all its requests.
package unassigned // import "upspin.io/dir/unassigned"

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Server implements upspin.DirServer.
type Server struct {
	endpoint upspin.Endpoint
}

var _ upspin.DirServer = Server{}

var unassignedErr = errors.Str("request to unassigned service")

// Glob implements upspin.DirServer.Glob.
func (Server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op errors.Op = "dir/Server.Glob"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Put implements upspin.DirServer.Put.
func (Server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/Server.Put"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (Server) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/Server.WhichAccess"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Delete implements upspin.DirServer.Delete.
func (Server) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/Server.Delete"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Lookup implements upspin.DirServer.Lookup.
func (Server) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op errors.Op = "dir/Server.Lookup"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Watch implements upspin.DirServer.Watch.
func (Server) Watch(upspin.PathName, int64, <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}

// Endpoint implements upspin.Service.
func (u Server) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Close implements upspin.Service.
func (Server) Close() {
}

// Dial implements upspin.Service.
func (Server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "dir/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterDirServer(transport, Server{})
}
