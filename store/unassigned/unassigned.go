// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unassigned implements a store server that errors out all its requests.
package unassigned // import "upspin.io/store/unassigned"

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

var unassignedErr = errors.Str("request to unassigned service")

// Get implements upspin.StoreServer.Get.
func (Server) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op errors.Op = "store/Server.Get"
	return nil, nil, nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Put implements upspin.StoreServer.Put.
func (Server) Put(data []byte) (*upspin.Refdata, error) {
	const op errors.Op = "store/Server.Put"
	return nil, errors.E(op, errors.Invalid, unassignedErr)
}

// Delete implements upspin.StoreServer.Delete.
func (Server) Delete(ref upspin.Reference) error {
	const op errors.Op = "store/Server.Delete"
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
	const op errors.Op = "store/Server.Dial"
	if e.Transport != upspin.Unassigned {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}

	return Server{e}, nil
}

const transport = upspin.Unassigned

func init() {
	bind.RegisterStoreServer(transport, Server{})
}
