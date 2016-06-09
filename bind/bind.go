// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bind contains the global binding switch and its methods.
package bind

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"upspin.io/log"
	"upspin.io/upspin"
)

// dialKey is the key to the LRU caches that store dialed services.
type dialKey struct {
	context  upspin.Context
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

	userMap      = make(map[upspin.Transport]upspin.User)
	directoryMap = make(map[upspin.Transport]upspin.Directory)
	storeMap     = make(map[upspin.Transport]upspin.Store)

	// These caches hold <dialKey, *dialedService> for each respective service type.
	userDialCache      = make(dialCache)
	directoryDialCache = make(dialCache)
	storeDialCache     = make(dialCache)
	reverseLookup      = make(map[upspin.Service]dialKey)

	inflightDials = make(map[dialKey]*inflightDial)
)

const allowOverwrite = true // for documentation purposes

// RegisterUser registers a User interface for the transport.
// There must be no previous registration.
func RegisterUser(transport upspin.Transport, user upspin.User) error {
	return registerUser(transport, user, !allowOverwrite)
}

// ReregisterUser replaces the User interface for the transport.
func ReregisterUser(transport upspin.Transport, user upspin.User) error {
	return registerUser(transport, user, allowOverwrite)
}

func registerUser(transport upspin.Transport, user upspin.User, allowOverwrite bool) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := userMap[transport]
	if ok && !allowOverwrite {
		return fmt.Errorf("cannot override User interface: %v", transport)
	}
	userMap[transport] = user
	return nil
}

// RegisterDirectory registers a Directory interface for the transport.
// There must be no previous registration.
func RegisterDirectory(transport upspin.Transport, dir upspin.Directory) error {
	return registerDirectory(transport, dir, !allowOverwrite)
}

// ReregisterDirectory replaces the Directory interface for the transport.
func ReregisterDirectory(transport upspin.Transport, dir upspin.Directory) error {
	return registerDirectory(transport, dir, allowOverwrite)
}

func registerDirectory(transport upspin.Transport, dir upspin.Directory, allowOverwrite bool) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := directoryMap[transport]
	if ok && !allowOverwrite {
		return fmt.Errorf("cannot override Directory interface: %v", transport)
	}
	directoryMap[transport] = dir
	return nil
}

// RegisterStore registers a Store interface for the transport.
// There must be no previous registration.
func RegisterStore(transport upspin.Transport, store upspin.Store) error {
	return registerStore(transport, store, !allowOverwrite)
}

// ReregisterStore replaces a Store interface for the transport.
func ReregisterStore(transport upspin.Transport, store upspin.Store) error {
	return registerStore(transport, store, allowOverwrite)
}

func registerStore(transport upspin.Transport, store upspin.Store, allowOverwrite bool) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := storeMap[transport]
	if ok && !allowOverwrite {
		return fmt.Errorf("cannot override Store interface: %v", transport)
	}
	storeMap[transport] = store
	return nil
}

// User returns a User interface bound to the endpoint.
func User(cc *upspin.Context, e upspin.Endpoint) (upspin.User, error) {
	mu.Lock()
	u, ok := userMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("User service with transport %q not registered", e.Transport)
	}
	x, err := reachableService(cc, e, userDialCache, u)
	if err != nil {
		return nil, err
	}
	return x.(upspin.User), nil
}

// Store returns a Store interface bound to the endpoint.
func Store(cc *upspin.Context, e upspin.Endpoint) (upspin.Store, error) {
	mu.Lock()
	s, ok := storeMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("Store service with transport %q not registered", e.Transport)
	}
	x, err := reachableService(cc, e, storeDialCache, s)
	if err != nil {
		return nil, err
	}
	return x.(upspin.Store), nil
}

// Directory returns a Directory interface bound to the endpoint.
func Directory(cc *upspin.Context, e upspin.Endpoint) (upspin.Directory, error) {
	mu.Lock()
	d, ok := directoryMap[e.Transport]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("Directory service with transport %q not registered", e.Transport)
	}
	x, err := reachableService(cc, e, directoryDialCache, d)
	if err != nil {
		return nil, err
	}
	return x.(upspin.Directory), nil
}

// Release closes the service and releases all resources associated with it.
func Release(service upspin.Service) error {
	mu.Lock()
	defer mu.Unlock()

	key, ok := reverseLookup[service]
	if !ok {
		return errors.New("service not found")
	}
	switch service.(type) {
	case upspin.Directory:
		delete(directoryDialCache, key)
	case upspin.Store:
		delete(storeDialCache, key)
	case upspin.User:
		delete(userDialCache, key)
	default:
		return errors.New("invalid service type")
	}
	service.Close()
	delete(reverseLookup, service)
	return nil
}

// reachableService finds a bound and reachable service in the cache or dials a fresh one and saves it in the cache.
func reachableService(cc *upspin.Context, e upspin.Endpoint, cache dialCache, dialer upspin.Dialer) (upspin.Service, error) {
	key := dialKey{
		context:  *cc,
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
			log.Printf("Bind: waiting for dial for key: %v", key)
			dial.Wait()
			log.Printf("Bind: dial completed for key: %v", key)
			return dial.service, dial.err
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
		log.Printf("Bind: server is dead for key: %v. Trying again.", key)
		if err := Release(ds.service); err != nil {
			return nil, fmt.Errorf("Releasing cached service: %v", err)
		}

		if n > 100 {
			// This should only happen if something is very wrong.
			panic("too many iterations looking for cached service")
		}
	}

	var err error
	ds = new(dialedService)
	ds.service, err = dialer.Dial(&key.context, key.endpoint)
	if err == nil && !ds.ping() {
		// The dial succeeded, but ping did not, so return an error.
		err = errors.New("Ping failed")
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

	return dial.service, dial.err
}
