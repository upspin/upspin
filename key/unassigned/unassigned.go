// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unassigned implements a store server that errors out all its requests.
package unassigned // import "upspin.io/key/unassigned"

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

var unassignedErr = errors.Str("request to unassigned service")

// Lookup implements upspin.KeysServer.Lookup.
func (Server) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op errors.Op = "key/Server.Lookup"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Put implements upspin.KeysServer.Put.
func (Server) Put(user *upspin.User) error {
	const op errors.Op = "key/Server.Put"
	return errors.E(op, errors.Invalid, unassignedErr)
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
	const op errors.Op = "key/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterKeyServer(transport, Server{})
}
