// Package bind contains the global binding switch and its methods.
package bind

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"upspin.googlesource.com/upspin.git/upspin"
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

// ping will issue a Ping through the dialled service, but only if it is not
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
// with the same dialKey will share a signel inflightDial value.
type inflightDial struct {
	sync.WaitGroup
	// The return values of a reachableService call.
	service upspin.Service
	err     error
}

type dialCache map[dialKey]*dialedService

const (
	pingFreshnessDuration = time.Minute * 15
)

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

	// This map holds any services that are being dialled.
	inflightDials = make(map[dialKey]*inflightDial)
)

// RegisterUser registers a User interface for the transport.
func RegisterUser(transport upspin.Transport, user upspin.User) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := userMap[transport]
	if ok {
		return fmt.Errorf("cannot override User interface: %v", transport)
	}
	userMap[transport] = user
	return nil
}

// RegisterDirectory registers a Directory interface for the transport.
func RegisterDirectory(transport upspin.Transport, dir upspin.Directory) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := directoryMap[transport]
	if ok {
		return fmt.Errorf("cannot override Directory interface: %v", transport)
	}
	directoryMap[transport] = dir
	return nil
}

// RegisterStore registers a Store interface for the transport.
func RegisterStore(transport upspin.Transport, store upspin.Store) error {
	mu.Lock()
	defer mu.Unlock()
	_, ok := storeMap[transport]
	if ok {
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
		wait   bool // Are we waiting for a concurrent dial (with the same dialKey) to complete?
	)
	for {
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

		if cached {
			// A cached service exists.
			if ds.ping() {
				// It's live; use it.
				return ds.service, nil
			}
			// It's dead; release it and try again.
			Release(ds.service)
			continue
		}
		break
	}

	// We don't have an active service, dial one now.
	if wait {
		// This call is waiting for a concurrent dial to complete
		// and will use its result.
		dial.Wait()
		return dial.service, dial.err
	}

	// This call is doing the actual dial.
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
