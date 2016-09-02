// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfixtures

import (
	"log"

	"upspin.io/upspin"
)

var _ upspin.Context = ServiceContext{}

// ServiceContext is an upspin.Context implementation that permits
// returning specific services.
type ServiceContext struct {
	upspin.Context

	// Key specifies the KeyServer to return from the KeyServer method.
	// If nil, KeyServer returns the result of the embedded Context's
	// KeyServer method.
	Key upspin.KeyServer

	// Store specifies the StoreServer to return from the StoreServer and
	// StoreServerFor methods. If nil, the methods return the result of
	// the embedded Context's methods.
	Store upspin.StoreServer

	// Dir specifies the DirServer to return from the DirServer method.
	// If nil, DirServer returns the result of the embedded Context's
	// DirServer method.
	Dir upspin.DirServer
}

func (c ServiceContext) KeyServer() upspin.KeyServer {
	if c.Key != nil {
		return c.Key
	}
	return c.Context.KeyServer()
}

func (c ServiceContext) StoreServer() upspin.StoreServer {
	if c.Store != nil {
		return c.Store
	}
	return c.Context.StoreServer()
}

func (c ServiceContext) StoreServerFor(e upspin.Endpoint) (upspin.StoreServer, error) {
	if c.Store != nil {
		return c.Store, nil
	}
	return c.Context.StoreServerFor(e)
}

func (c ServiceContext) DirServer(p upspin.PathName) upspin.DirServer {
	log.Println("ServiceContext.DirServer", p)
	if c.Dir != nil {
		return c.Dir
	}
	return c.Context.DirServer(p)
}
