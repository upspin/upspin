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
func NewSimpleContext() upspin.Context {
	return &simpleContext{
		userName: "noone@nowhere.org",
	}
}

// User implements upspin.Context.
func (ctx *simpleContext) User() upspin.User {
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

// UserName implements upspin.Context.
func (ctx *simpleContext) UserName() upspin.UserName {
	return ctx.userName
}

// SetUserName implements upspin.Context.
func (ctx *simpleContext) SetUserName(u upspin.UserName) upspin.Context {
	return ctx
}

// Factotum implements upspin.Context.
func (ctx *simpleContext) Factotum() upspin.Factotum {
	return nil
}

// SetFactotum implements upspin.Context.
func (ctx *simpleContext) SetFactotum(f upspin.Factotum) upspin.Context {
	return ctx
}

// Packing implements upspin.Context.
func (ctx *simpleContext) Packing() upspin.Packing {
	return upspin.PlainPack
}

// SetPacking implements upspin.Context.
func (ctx *simpleContext) SetPacking(p upspin.Packing) upspin.Context {
	return ctx
}

// UserEndpoint implements upspin.Context.
func (ctx *simpleContext) UserEndpoint() upspin.Endpoint {
	return ep0
}

// SetUserEndpoint implements upspin.Context.
func (ctx *simpleContext) SetUserEndpoint(e upspin.Endpoint) upspin.Context {
	return ctx
}

// DirEndpoint implements upspin.Context.
func (ctx *simpleContext) DirEndpoint() upspin.Endpoint {
	return ep0
}

// SetEndpoint implements upspin.Context.
func (ctx *simpleContext) SetDirEndpoint(e upspin.Endpoint) upspin.Context {
	return ctx
}

// StoreEndpoint implements upspin.Context.
func (ctx *simpleContext) StoreEndpoint() upspin.Endpoint {
	return ep0
}

// SetStoreEndpoint implements upspin.Context.
func (ctx *simpleContext) SetStoreEndpoint(e upspin.Endpoint) upspin.Context {
	return ctx
}

// Copy implements upspin.Context.
func (ctx *simpleContext) Copy() upspin.Context {
	c := *ctx
	return &c
}
