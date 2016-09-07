// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfixtures

import (
	"upspin.io/errors"
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
		svc, err := c.Key.Dial(c, c.KeyEndpoint())
		if err != nil {
			panic("ServiceContext: KeyServer: " + err.Error())
		}
		return svc.(upspin.KeyServer)
	}
	return c.Context.KeyServer()
}

func (c ServiceContext) StoreServer() upspin.StoreServer {
	if c.Store != nil {
		svc, err := c.Store.Dial(c, c.StoreEndpoint())
		if err != nil {
			panic("ServiceContext: StoreServer: " + err.Error())
		}
		return svc.(upspin.StoreServer)
	}
	return c.Context.StoreServer()
}

func (c ServiceContext) StoreServerFor(e upspin.Endpoint) (upspin.StoreServer, error) {
	if c.Store != nil {
		svc, err := c.Store.Dial(c, e)
		if err != nil {
			return nil, errors.Errorf("ServiceContext: StoreServerFor: %v", err)
		}
		return svc.(upspin.StoreServer), nil
	}
	return c.Context.StoreServerFor(e)
}

func (c ServiceContext) DirServer(p upspin.PathName) upspin.DirServer {
	if c.Dir != nil {
		svc, err := c.Dir.Dial(c, c.DirEndpoint())
		if err != nil {
			panic("ServiceContext: DirServer: " + err.Error())
		}
		return svc.(upspin.DirServer)
	}
	return c.Context.DirServer(p)
}
