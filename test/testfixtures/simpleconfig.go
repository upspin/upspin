// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfixtures // import "upspin.io/test/testfixtures"

import "upspin.io/upspin"

type simpleConfig struct {
	userName upspin.UserName
}

var ep0 upspin.Endpoint // Will have upspin.Unassigned as transport.

// NewSimpleConfig returns a config with nothing but a user name.
func NewSimpleConfig(u upspin.UserName) upspin.Config {
	return &simpleConfig{userName: u}
}

// KeyServer implements upspin.Config.
func (cfg *simpleConfig) KeyServer() upspin.KeyServer {
	return nil
}

// DirServer implements upspin.Config.
func (cfg *simpleConfig) DirServer(name upspin.PathName) upspin.DirServer {
	return nil
}

// StoreServer implements upspin.Config.
func (cfg *simpleConfig) StoreServer() upspin.StoreServer {
	return nil
}

func (cfg *simpleConfig) StoreServerFor(upspin.Endpoint) (upspin.StoreServer, error) {
	return nil, nil
}

// UserName implements upspin.Config.
func (cfg *simpleConfig) UserName() upspin.UserName {
	return cfg.userName
}

// Factotum implements upspin.Config.
func (cfg *simpleConfig) Factotum() upspin.Factotum {
	return nil
}

// Packing implements upspin.Config.
func (cfg *simpleConfig) Packing() upspin.Packing {
	return upspin.PlainPack
}

// UserEndpoint implements upspin.Config.
func (cfg *simpleConfig) KeyEndpoint() upspin.Endpoint {
	return ep0
}

// DirEndpoint implements upspin.Config.
func (cfg *simpleConfig) DirEndpoint() upspin.Endpoint {
	return ep0
}

// StoreEndpoint implements upspin.Config.
func (cfg *simpleConfig) StoreEndpoint() upspin.Endpoint {
	return ep0
}

// CacheEndpoint implements upspin.Config.
func (cfg *simpleConfig) CacheEndpoint() upspin.Endpoint {
	return ep0
}

// Value implements upspin.Config.
func (cfg *simpleConfig) Value(string) string {
	return ""
}
