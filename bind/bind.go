// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bind contains the global binding switch and its methods.
package bind // import "upspin.io/bind"

import (
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/user"
)

type dialKey struct {
	user        upspin.UserName
	endpoint    upspin.Endpoint
	cacheserver upspin.Endpoint
}

type dialers map[upspin.Transport]upspin.Dialer
type services map[dialKey]upspin.Service

// The servers struct tracks upspin.Dialers that have been registered for
// various transports, and upspin.Services that have already been
// successfully dialed.
type servers struct {
	kind     string
	mu       sync.Mutex // Guards the variables below.
	dialers  dialers
	services services
}

var (
	noCache bool // For testing. When true, the caches are not used.

	keyServers = servers{
		kind:     "Key",
		dialers:  make(dialers),
		services: make(services),
	}
	dirServers = servers{
		kind:     "Directory",
		dialers:  make(dialers),
		services: make(services),
	}
	storeServers = servers{
		kind:     "Store",
		dialers:  make(dialers),
		services: make(services),
	}
)

// RegisterKeyServer registers a KeyServer interface for the transport.
// There must be no previous registration.
func RegisterKeyServer(transport upspin.Transport, key upspin.KeyServer) error {
	return keyServers.register(transport, key)
}

// RegisterDirServer registers a DirServer interface for the transport.
// There must be no previous registration.
func RegisterDirServer(transport upspin.Transport, dir upspin.DirServer) error {
	return dirServers.register(transport, dir)
}

// RegisterStoreServer registers a StoreServer interface for the transport.
// There must be no previous registration.
func RegisterStoreServer(transport upspin.Transport, store upspin.StoreServer) error {
	return storeServers.register(transport, store)
}

// KeyServer returns a KeyServer interface bound to the endpoint.
func KeyServer(cc upspin.Config, e upspin.Endpoint) (upspin.KeyServer, error) {
	x, err := keyServers.reachableService(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.KeyServer), nil
}

// StoreServer returns a StoreServer interface bound to the endpoint.
func StoreServer(cc upspin.Config, e upspin.Endpoint) (upspin.StoreServer, error) {
	x, err := storeServers.reachableService(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.StoreServer), nil
}

// DirServer returns a DirServer interface bound to the endpoint.
func DirServer(cc upspin.Config, e upspin.Endpoint) (upspin.DirServer, error) {
	x, err := dirServers.reachableService(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.DirServer), nil
}

// DirServer returns a DirServer interface bound to the endpoint that serves
// the given user. If the name is empty, it returns the directory endpoint
// in the config.
func DirServerFor(cc upspin.Config, userName upspin.UserName) (upspin.DirServer, error) {
	const op errors.Op = "bind.DirServerFor"
	if userName == "" {
		// If name is empty just return the directory at cc.DirEndpoint().
		d, err := DirServer(cc, cc.DirEndpoint())
		if err != nil {
			return nil, errors.E(op, err)
		}
		return d, nil
	}
	// Just a safety check; shouldn't be necessary.
	userName, err := user.Clean(userName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	key, err := KeyServer(cc, cc.KeyEndpoint())
	if err != nil {
		return nil, errors.E(op, err)
	}
	u, err := key.Lookup(userName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	var endpoints []upspin.Endpoint
	endpoints = append(endpoints, u.Dirs...)
	var firstErr error
	for _, e := range endpoints {
		d, err := DirServer(cc, e)
		if err == nil {
			return d, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, errors.E(op, firstErr)
	}
	return nil, errors.E(op, userName, "no directory endpoints found")

}

func (s *servers) register(transport upspin.Transport, key upspin.Dialer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.dialers[transport]
	if ok {
		return errors.E(s.registerOp(), errors.Invalid, errors.Errorf("server already registered for transport %v", transport))
	}
	s.dialers[transport] = key
	return nil
}

// reachableService finds a bound and reachable service in the cache or dials a
// fresh one and saves it in the cache.
func (s *servers) reachableService(cc upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	key := dialKey{user: cc.UserName(), endpoint: e, cacheserver: cc.CacheEndpoint()}
	s.mu.Lock()
	defer s.mu.Unlock()
	service, cached := s.services[key]
	if !cached {
		var err error
		dialer, ok := s.dialers[e.Transport]
		if !ok {
			return nil, errors.E(s.serverOp(), errors.Invalid, errors.Errorf("service with transport %q not registered", e.Transport))
		}
		service, err = dialer.Dial(cc, e)
		if err != nil {
			return nil, errors.E(s.serverOp(), err)
		}
		if !noCache {
			s.services[key] = service
		}
	}
	return service, nil
}

func (s *servers) registerOp() errors.Op {
	return errors.Op("bind.Register" + s.kind + "Server") // "bind.RegisterKeyServer"
}

func (s *servers) serverOp() errors.Op {
	return errors.Op("bind." + s.kind + "Server") // "bind.KeyServer"
}
