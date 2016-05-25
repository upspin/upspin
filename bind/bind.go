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

	mu         sync.Mutex // Protects the Ping operation on service and the fields below
	lastPing   time.Time
	generation uint32
}

type dialCache map[dialKey]*dialedService

var (
	pingFreshnessDuration = time.Minute * 15 // A var so it can be tweaked for testing

	mu sync.Mutex // Protects all fields below

	userMap      = make(map[upspin.Transport]upspin.User)
	directoryMap = make(map[upspin.Transport]upspin.Directory)
	storeMap     = make(map[upspin.Transport]upspin.Store)

	// These caches hold <dialKey, *dialedService> for each respective service type.
	userDialCache      = make(dialCache)
	directoryDialCache = make(dialCache)
	storeDialCache     = make(dialCache)
	reverseLookup      = make(map[upspin.Service]dialKey)

	generation uint32
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
	mu.Lock()
	generation++
	ds, found := cache[key]
	var service upspin.Service
	var err error
	if found {
		ds.mu.Lock()
		service = ds.service
		if ds.lastPing.Add(pingFreshnessDuration).After(time.Now()) {
			// Last ping is fresh.
			ds.mu.Unlock() // Order of unlocking is important here.
			mu.Unlock()
			return service, nil
		}
		// Must re-ping and store the new ping time.
		ds.mu.Unlock() // Order is important.
		mu.Unlock()
	} else {
		// mu is held
		service, err = dialer.Dial(&key.context, key.endpoint)
		if err != nil {
			return nil, err
		}
		ds = &dialedService{
			service:    service,
			lastPing:   time.Now(),
			generation: generation,
		}
		ds.mu.Lock()
		cache[key] = ds
		reverseLookup[service] = key
		ds.mu.Unlock() // Order is important.
		mu.Unlock()
	}
	// Not found or found but not reachable. Must ping again and update cache.
	ds.mu.Lock()
	if !service.Ping() {
		return nil, errors.New("Ping failed")
	}
	pingGen := ds.generation
	ds.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if pingGen != generation {
		return nil, errors.New("concurrent bind") // likely the other bind has succeeded.
	}
	return service, nil
}
