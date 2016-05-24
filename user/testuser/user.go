// Package testuser implements a non-persistent, memory-resident user service.
package testuser

import (
	"errors"
	"fmt"
	"sync"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service maps user names to potential machines holding root of the user's tree.
// There is one for each Dial call, but they all share the underlying database.
// It implements the upspin.User interface.
type Service struct {
	upspin.NoConfiguration
	// context holds the context that created the call.
	context upspin.Context
	db      *database
}

// A database holds the information for the known users.
// There is one instance, created in init, shared by all Service objects.
type database struct {
	endpoint upspin.Endpoint
	// mu protects the fields below.
	mu sync.RWMutex
	// serviceOwner identifies the user running the user service. TODO: unused.
	serviceOwner upspin.UserName
	// serviceCache maintains a cache of existing service objects.
	// Note the key is by value, so multiple equivalent contexts will end up
	// with the same service.
	serviceCache map[upspin.Context]*Service
	root         map[upspin.UserName][]upspin.Endpoint
	keystore     map[upspin.UserName][]upspin.PublicKey
}

var _ upspin.User = (*Service)(nil)

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are
// fallbacks and the user's public keys, if known.
func (s *Service) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	// Return copies so the caller can't modify our data structures.
	locs := make([]upspin.Endpoint, len(s.db.root[name]))
	copy(locs, s.db.root[name])
	keys := make([]upspin.PublicKey, len(s.db.keystore[name]))
	copy(keys, s.db.keystore[name])
	return locs, keys, nil
}

// SetPublicKeys sets a slice of public keys to the keystore for a
// given user name. Previously-known keys for that user are
// forgotten. To add keys to the existing set, Lookup and append to
// the slice. If keys is nil, the user is forgotten.
func (s *Service) SetPublicKeys(name upspin.UserName, keys []upspin.PublicKey) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if keys == nil {
		delete(s.db.keystore, name)
	} else {
		s.db.keystore[name] = keys
	}
}

// ListUsers returns a slice of all known users with at least one public key.
func (s *Service) ListUsers() []upspin.UserName {
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	users := make([]upspin.UserName, 0, len(s.db.keystore))
	for u := range s.db.keystore {
		users = append(users, u)
	}
	return users
}

// validateUserName returns a parsed path if the username is valid.
func validateUserName(name upspin.UserName) (*path.Parsed, error) {
	parsed, err := path.Parse(upspin.PathName(name))
	if err != nil {
		return nil, err
	}
	if !parsed.IsRoot() {
		return nil, fmt.Errorf("testuser: %q not a user name", name)
	}
	return &parsed, nil
}

// Install installs a user and its.db.root in the provided Directory
// service. For a real User service, this would be done by some offline
// administrative procedure. For this test version, we just provide a
// simple hook for testing.
func (s *Service) Install(name upspin.UserName, dir upspin.Directory) error {
	// Verify that it is a valid name.
	parsed, err := validateUserName(name)
	if err != nil {
		return err
	}
	loc, err := dir.MakeDirectory(upspin.PathName(parsed.User() + "/"))
	if err != nil {
		return err
	}
	s.innerAddRoot(parsed.User(), loc.Endpoint)
	return nil
}

// innerAddRoot adds a root for the user, which must be a parsed, valid Upspin user name.
func (s *Service) innerAddRoot(userName upspin.UserName, endpoint upspin.Endpoint) {
	s.db.mu.Lock()
	s.db.root[userName] = append(s.db.root[userName], endpoint)
	s.db.mu.Unlock()
}

// AddRoot adds an endpoint as the user's.db.root endpoint.
func (s *Service) AddRoot(name upspin.UserName, endpoint upspin.Endpoint) error {
	// Verify that it is a valid name.
	parsed, err := validateUserName(name)
	if err != nil {
		return err
	}
	s.innerAddRoot(parsed.User(), endpoint)
	return nil
}

// Methods to implement upspin.Service

func (s *Service) Endpoint() upspin.Endpoint {
	return s.db.endpoint
}

// ServerUserName implements upspin.service.
func (s *Service) ServerUserName() string {
	return "testuser"
}

// Dial always returns the same instance of the service. The Transport must be InProcess
// but the NetAddr is ignored.
func (s *Service) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("testuser: unrecognized transport")
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.serviceOwner == "" {
		// This is the first call; set the owner and endpoint.
		s.db.endpoint = e
		s.db.serviceOwner = context.UserName
	}
	// Is there already a service for this user?
	if this := s.db.serviceCache[*context]; this != nil {
		return this, nil
	}
	this := *s // Make a copy.
	this.context = *context
	s.db.serviceCache[*context] = &this
	return &this, nil
}

// Ping implements upspin.Service.
func (s *Service) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *Service) Close() {
}

// Authenticate implements upspin.Service.
func (s *Service) Authenticate(*upspin.Context) error {
	return nil
}

func init() {
	s := &Service{
		db: &database{
			endpoint: upspin.Endpoint{
				Transport: upspin.InProcess,
				NetAddr:   "", // Ignored.
			},
			serviceCache: make(map[upspin.Context]*Service),
			root:         make(map[upspin.UserName][]upspin.Endpoint),
			keystore:     make(map[upspin.UserName][]upspin.PublicKey),
		},
	}
	bind.RegisterUser(upspin.InProcess, s)
}
