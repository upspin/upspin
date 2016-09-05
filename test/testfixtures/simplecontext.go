// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfixtures

import "upspin.io/upspin"

type simpleContext struct {
	userName upspin.UserName
}

var ep0 upspin.Endpoint // Will have upspin.Unassigned as transport.

// NewSimpleContext returns a context with nothing but a user name.
func NewSimpleContext(u upspin.UserName) upspin.Context {
	return &simpleContext{userName: u}
}

// KeyServer implements upspin.Context.
func (ctx *simpleContext) KeyServer() upspin.KeyServer {
	return nil
}

// DirServer implements upspin.Context.
func (ctx *simpleContext) DirServer(name upspin.PathName) upspin.DirServer {
	return nil
}

// StoreServer implements upspin.Context.
func (ctx *simpleContext) StoreServer() upspin.StoreServer {
	return nil
}

func (ctx *simpleContext) StoreServerFor(upspin.Endpoint) (upspin.StoreServer, error) {
	return nil, nil
}

// UserName implements upspin.Context.
func (ctx *simpleContext) UserName() upspin.UserName {
	return ctx.userName
}

// Factotum implements upspin.Context.
func (ctx *simpleContext) Factotum() upspin.Factotum {
	return nil
}

// Packing implements upspin.Context.
func (ctx *simpleContext) Packing() upspin.Packing {
	return upspin.PlainPack
}

// UserEndpoint implements upspin.Context.
func (ctx *simpleContext) KeyEndpoint() upspin.Endpoint {
	return ep0
}

// DirEndpoint implements upspin.Context.
func (ctx *simpleContext) DirEndpoint() upspin.Endpoint {
	return ep0
}

// StoreEndpoint implements upspin.Context.
func (ctx *simpleContext) StoreEndpoint() upspin.Endpoint {
	return ep0
}

// StoreCacheEndpoint implements upspin.Context.
func (ctx *simpleContext) StoreCacheEndpoint() upspin.Endpoint {
	return ep0
}
