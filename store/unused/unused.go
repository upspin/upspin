// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unused implements a store server that errors out all its requests.
package unused

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// unused implements upspin.Store.
type unused struct {
	endpoint upspin.Endpoint
}

var _ upspin.Store = (*unused)(nil)

var unusedErr = errors.Str("request to 'unused' service")

// Get implements upspin.Store.Get.
func (*unused) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	return nil, nil, errors.E("Get", errors.Invalid, unusedErr)
}

// Put implements upspin.Store.Put.
func (*unused) Put(data []byte) (upspin.Reference, error) {
	return "", errors.E("Put", errors.Invalid, unusedErr)
}

// Delete implements upspin.Store.Delete.
func (*unused) Delete(ref upspin.Reference) error {
	return errors.E("Delete", errors.Invalid, unusedErr)
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
	bind.RegisterStore(transport, &unused{})
}
