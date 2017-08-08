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
	user     upspin.UserName
	endpoint upspin.Endpoint
}

type dialCache map[dialKey]upspin.Service

var (
	noCache bool // For testing. When true, the caches are not used.

	mu sync.Mutex // Guards the variables below.

	keyMap       = make(map[upspin.Transport]upspin.KeyServer)
	directoryMap = make(map[upspin.Transport]upspin.DirServer)
	storeMap     = make(map[upspin.Transport]upspin.StoreServer)

	keyDialCache       = make(dialCache)
	directoryDialCache = make(dialCache)
	storeDialCache     = make(dialCache)
)

// RegisterKeyServer registers a KeyServer interface for the transport.
// There must be no previous registration.
func RegisterKeyServer(transport upspin.Transport, key upspin.KeyServer) error {
	const op = "bind.RegisterKeyServer"
	mu.Lock()
	defer mu.Unlock()
	_, ok := keyMap[transport]
	if ok {
		return errors.E(op, errors.Invalid, errors.Errorf("server already registered for transport %v", transport))
	}
	keyMap[transport] = key
	return nil
}

// RegisterDirServer registers a DirServer interface for the transport.
// There must be no previous registration.
func RegisterDirServer(transport upspin.Transport, dir upspin.DirServer) error {
	const op = "bind.RegisterDirServer"
	mu.Lock()
	defer mu.Unlock()
	_, ok := directoryMap[transport]
	if ok {
		return errors.E(op, errors.Invalid, errors.Errorf("server already registered for transport %v", transport))
	}
	directoryMap[transport] = dir
	return nil
}

// RegisterStoreServer registers a StoreServer interface for the transport.
// There must be no previous registration.
func RegisterStoreServer(transport upspin.Transport, store upspin.StoreServer) error {
	const op = "bind.RegisterStoreServer"
	mu.Lock()
	defer mu.Unlock()
	_, ok := storeMap[transport]
	if ok {
		return errors.E(op, errors.Invalid, errors.Errorf("server already registered for transport %v", transport))
	}
	storeMap[transport] = store
	return nil
}

// KeyServer returns a KeyServer interface bound to the endpoint.
func KeyServer(cc upspin.Config, e upspin.Endpoint) (upspin.KeyServer, error) {
	const op = "bind.KeyServer"
	mu.Lock()
	u, ok := keyMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("service with transport %q not registered", e.Transport))
	}
	x, err := reachableService(cc, op, e, keyDialCache, u)
	if err != nil {
		return nil, err
	}
	return x.(upspin.KeyServer), nil
}

// StoreServer returns a StoreServer interface bound to the endpoint.
func StoreServer(cc upspin.Config, e upspin.Endpoint) (upspin.StoreServer, error) {
	const op = "bind.StoreServer"
	mu.Lock()
	s, ok := storeMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("service with transport %q not registered", e.Transport))
	}
	x, err := reachableService(cc, op, e, storeDialCache, s)
	if err != nil {
		return nil, err
	}
	return x.(upspin.StoreServer), nil
}

// DirServer returns a DirServer interface bound to the endpoint.
func DirServer(cc upspin.Config, e upspin.Endpoint) (upspin.DirServer, error) {
	const op = "bind.DirServer"
	mu.Lock()
	d, ok := directoryMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("service with transport %q not registered", e.Transport))
	}
	x, err := reachableService(cc, op, e, directoryDialCache, d)
	if err != nil {
		return nil, err
	}
	return x.(upspin.DirServer), nil
}

// DirServer returns a DirServer interface bound to the endpoint that serves
// the given user. If the name is empty, it returns the directory endpoint
// in the config.
func DirServerFor(cc upspin.Config, userName upspin.UserName) (upspin.DirServer, error) {
	const op = "bind.DirServerFor"
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
	return nil, errors.E(op, userName, errors.Str("no directory endpoints found"))

}

// reachableService finds a bound and reachable service in the cache or dials a
// fresh one and saves it in the cache.
func reachableService(cc upspin.Config, op string, e upspin.Endpoint, cache dialCache, dialer upspin.Dialer) (upspin.Service, error) {
	if noCache {
		return dialer.Dial(cc, e)
	}
	key := dialKey{user: cc.UserName(), endpoint: e}
	mu.Lock()
	defer mu.Unlock()
	service, cached := cache[key]
	if !cached {
		var err error
		service, err = dialer.Dial(cc, e)
		if err != nil {
			return nil, errors.E(op, err)
		}
		cache[key] = service
	}
	return service, nil
}

// NoCache supresses the caching of dial results. This was added for
// debugging.
func NoCache() {
	noCache = true
}
