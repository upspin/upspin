// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bind contains the global binding switch and its methods.
package bind

import (
	"sync"
	"time"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// dialKey is the key to the LRU caches that store dialed services.
type dialKey struct {
	user     upspin.UserName
	endpoint upspin.Endpoint
}

// dialedService holds a dialed service and its last ping time.
type dialedService struct {
	service upspin.Service

	mu       sync.Mutex
	lastPing time.Time
	dead     bool
}

// A variable, so that it can be tweaked during tests.
var pingFreshnessDuration = 5 * time.Minute

// ping will issue a Ping through the dialed service, but only if it is not
// dead and its last ping time is more than pingFreshnessDuration ago.
// If the ping fails the service is marked as dead.
func (ds *dialedService) ping() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.dead {
		return false
	}
	now := time.Now()
	if ds.lastPing.Add(pingFreshnessDuration).After(now) {
		// Last ping is fresh, don't ping again.
		return true
	}
	// Must re-ping and store the new ping time.
	if ds.service.Ping() {
		ds.lastPing = now
		return true
	}
	// Connection is dead.
	ds.dead = true
	return false
}

// inflightDial represents a service that is being created by the
// reachableService function. Concurrent calls to reachableService
// with the same dialKey will share a single inflightDial value.
type inflightDial struct {
	sync.WaitGroup

	// These are the return values of a reachableService call.
	// Concurrent calls to reachableService with the same context
	// and endpoint should return the same values.
	// Either service or err must be non-nil.
	service upspin.Service
	err     error
}

type dialCache map[dialKey]*dialedService

var (
	mu sync.Mutex // Guards the variables below.

	userMap      = make(map[upspin.Transport]upspin.KeyServer)
	directoryMap = make(map[upspin.Transport]upspin.DirServer)
	storeMap     = make(map[upspin.Transport]upspin.StoreServer)

	// These caches hold <dialKey, *dialedService> for each respective service type.
	userDialCache      = make(dialCache)
	directoryDialCache = make(dialCache)
	storeDialCache     = make(dialCache)
	reverseLookup      = make(map[upspin.Service]dialKey)

	inflightDials = make(map[dialKey]*inflightDial)
)

const allowOverwrite = true // for documentation purposes

// RegisterKeyServer registers a KeyServer interface for the transport.
// There must be no previous registration.
func RegisterKeyServer(transport upspin.Transport, user upspin.KeyServer) error {
	const op = "bind.RegisterKeyServer"
	mu.Lock()
	defer mu.Unlock()
	_, ok := userMap[transport]
	if ok {
		return errors.E(op, errors.Invalid, errors.Errorf("server already registered for transport %v", transport))
	}
	userMap[transport] = user
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
func KeyServer(cc upspin.Context, e upspin.Endpoint) (upspin.KeyServer, error) {
	const op = "bind.KeyServer"
	mu.Lock()
	u, ok := userMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("service with transport %q not registered", e.Transport))
	}
	x, err := reachableService(cc, op, e, userDialCache, u)
	if err != nil {
		return nil, err
	}
	return x.(upspin.KeyServer), nil
}

// StoreServer returns a StoreServer interface bound to the endpoint.
func StoreServer(cc upspin.Context, e upspin.Endpoint) (upspin.StoreServer, error) {
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
func DirServer(cc upspin.Context, e upspin.Endpoint) (upspin.DirServer, error) {
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

// Release closes the service and releases all resources associated with it.
func Release(service upspin.Service) error {
	const op = "bind.Release"
	mu.Lock()
	defer mu.Unlock()

	key, ok := reverseLookup[service]
	if !ok {
		return errors.E(op, errors.NotExist, errors.Str("service not found"))
	}
	switch service.(type) {
	case upspin.DirServer:
		delete(directoryDialCache, key)
	case upspin.StoreServer:
		delete(storeDialCache, key)
	case upspin.KeyServer:
		delete(userDialCache, key)
	default:
		return errors.E(op, errors.Invalid, errors.Errorf("unknown service type %T", service))
	}
	service.Close()
	delete(reverseLookup, service)
	return nil
}

// reachableService finds a bound and reachable service in the cache or dials a fresh one and saves it in the cache.
func reachableService(cc upspin.Context, op string, e upspin.Endpoint, cache dialCache, dialer upspin.Dialer) (upspin.Service, error) {
	key := dialKey{
		user:     cc.UserName(),
		endpoint: e,
	}

	var (
		ds     *dialedService
		cached bool // Was there a service in the cache?
		dial   *inflightDial
	)
	for n := 0; ; n++ {
		var wait bool // Are we waiting for a concurrent dial (with the same dialKey)?

		mu.Lock()
		ds, cached = cache[key]
		if !cached {
			dial, wait = inflightDials[key]
			if !wait {
				dial = new(inflightDial)
				dial.Add(1)
				inflightDials[key] = dial
			}
		}
		mu.Unlock()

		if wait {
			// This call is waiting for a concurrent dial to complete
			// and will use its result.
			dial.Wait()
			if dial.err != nil {
				return nil, errors.E(op, dial.err)
			}
			return dial.service, nil
		}

		if !cached {
			// No cached service or concurrent dial, so dial one.
			break
		}

		// A cached service exists.
		if ds.ping() {
			// It's live; use it.
			return ds.service, nil
		}
		// It's dead; release it and try again.
		if err := Release(ds.service); err != nil {
			return nil, errors.E(op, errors.IO, errors.Errorf("Releasing cached service: %v", err))
		}

		if n > 100 {
			// This should only happen if something is very wrong.
			panic("too many iterations looking for cached service")
		}
	}

	var err error
	ds = new(dialedService)
	ds.service, err = dialer.Dial(cc, key.endpoint)
	if err == nil && !ds.ping() {
		// The dial succeeded, but ping did not, so return an error.
		err = errors.Str("Ping failed")
	}

	mu.Lock()
	defer mu.Unlock()

	// Set up the return values for this call,
	// and any waiting concurrent calls.
	if err != nil {
		dial.err = err
	} else {
		dial.service = ds.service
		// Add the live service to the cache.
		cache[key] = ds
		reverseLookup[ds.service] = key
	}

	dial.Done()                // Wake any concurrent callers, as
	delete(inflightDials, key) // the dial is no longer in flight.

	if err != nil {
		return nil, errors.E(op, dial.err)
	}
	return dial.service, nil
}
