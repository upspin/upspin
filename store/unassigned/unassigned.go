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

// Server implements upspin.StoreServer.
type Server struct {
	endpoint upspin.Endpoint
}

var _ upspin.StoreServer = Server{}

var ServerErr = errors.Str("request to Server service")

// Get implements upspin.StoreServer.Get.
func (Server) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	const op = "store/Server.Get"
	return nil, nil, errors.E(op, errors.Invalid, ServerErr)
}

// Put implements upspin.StoreServer.Put.
func (Server) Put(data []byte) (upspin.Reference, error) {
	const op = "store/Server.Put"
	return "", errors.E(op, errors.Invalid, ServerErr)
}

// Delete implements upspin.StoreServer.Delete.
func (Server) Delete(ref upspin.Reference) error {
	const op = "store/Server.Delete"
	return errors.E(op, errors.Invalid, ServerErr)
}

// Endpoint implements upspin.Service.
func (u Server) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Configure implements upspin.Service.
func (Server) Configure(options ...string) (upspin.UserName, error) {
	const op = "store/Server.Configure"
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
	const op = "store/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterStoreServer(transport, Server{})
}
