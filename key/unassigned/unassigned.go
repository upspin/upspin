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

// Server implements upspin.KeyServer.
type Server struct {
	endpoint upspin.Endpoint
}

var _ upspin.KeyServer = Server{}

var ServerErr = errors.Str("request to Server service")

// Lookup implements upspin.KeysServer.Lookup.
func (Server) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "key/Server.Lookup"
	return nil, errors.E(op, errors.Invalid, ServerErr)
}

// Put implements upspin.KeysServer.Put.
func (Server) Put(user *upspin.User) error {
	const op = "key/Server.Put"
	return errors.E(op, errors.Invalid, ServerErr)
}

// Endpoint implements upspin.Service.
func (u Server) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (Server) Configure(options ...string) (upspin.UserName, error) {
	const op = "key/Server.Configure"
	return "", errors.E(op, errors.Invalid, ServerErr)
}

// Close implements upspin.Service.
func (Server) Close() {
}

// Authenticate implements upspin.Service.
func (Server) Authenticate(context upspin.Context) error {
	const op = "key/Server.Authenticate"
	return errors.E(op, errors.Invalid, ServerErr)
}

// Ping implements upspin.Service.
func (Server) Ping() bool {
	return true
}

// Dial implements upspin.Service.
func (Server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "key/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterKeyServer(transport, Server{})
}
