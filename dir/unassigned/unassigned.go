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

// Server implements upspin.DirServer.
type Server struct {
	endpoint upspin.Endpoint
}

var _ upspin.DirServer = Server{}

var ServerErr = errors.Str("request to Server service")

// Glob implements upspin.DirServer.Glob.
func (Server) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "dir/Server.Glob"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// MakeDirectory implements upspin.DirServer.MakeDirectory.
func (Server) MakeDirectory(directoryName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/Server.MakeDirectory"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// Put implements upspin.DirServer.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (Server) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/Server.Put"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (Server) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/Server.WhichAccess"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// Delete implements upspin.DirServer.Delete.
func (Server) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/Server.Delete"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// Lookup implements upspin.DirServer.Lookup.
func (Server) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/Server.Lookup"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// Endpoint implements upspin.Service.
func (u Server) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (Server) Configure(options ...string) (upspin.UserName, error) {
	const op = "dir/Server.Configure"
	return "", errors.E(op, errors.Invalid, ServerErr)
}

// Close implements upspin.Service.
func (Server) Close() {
}

// Ping implements upspin.Service.
func (Server) Ping() bool {
	return true
}

// Dial implements upspin.Service.
func (Server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "dir/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterDirServer(transport, Server{})
}
