// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
package remote

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// unused implements upspin.User.
type unused struct {
	endpoint upspin.Endpoint
}

var _ upspin.User = (*unused)(nil)

var unusedErr = errors.Str("request to 'unused' service")

// Lookup implements upspin.User.Lookup.
func (*unused) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	return nil, nil, errors.E("Lookup", errors.Invalid, unusedErr)
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
	bind.RegisterUser(transport, &unused{})
}
