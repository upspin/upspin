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

// unassigned implements upspin.User.
type unassigned struct {
	endpoint upspin.Endpoint
}

var _ upspin.User = (*unassigned)(nil)

var unassignedErr = errors.Str("request to unassigned service")

// Lookup implements upspin.User.Lookup.
func (*unassigned) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	return nil, nil, errors.E("Lookup", errors.Invalid, unassignedErr)
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
	bind.RegisterUser(transport, &unassigned{})
}
