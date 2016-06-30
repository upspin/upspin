// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testfixtures implements dummies for Store, Directory and User services for tests.
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

// Directory implements upspin.Context.
func (ctx *simpleContext) Directory(name upspin.PathName) upspin.Directory {
	return nil
}

// Store implements upspin.Context.
func (ctx *simpleContext) Store() upspin.Store {
	return nil
}

// Store implements upspin.Context.
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

// DirectoryEndpoint implements upspin.Context.
func (ctx *simpleContext) DirectoryEndpoint() upspin.Endpoint {
	return ep0
}

// SetDirectoryEndpoint implements upspin.Context.
func (ctx *simpleContext) SetDirectoryEndpoint(e upspin.Endpoint) upspin.Context {
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
