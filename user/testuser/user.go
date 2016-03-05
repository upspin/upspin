// Package testuser implements a non-persistent, memory-resident user service.
package testuser

import (
	"errors"
	"fmt"
	"sync"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service maps user names to potential machines holdining root of the user's tree.
// It implements the upspin.User interface.
type Service struct {
	m        sync.Mutex // Protects both fields below.
	root     map[upspin.UserName][]upspin.Endpoint
	keystore map[upspin.UserName][]upspin.PublicKey
}

var _ upspin.User = (*Service)(nil)

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are
// fallbacks and the user's public keys, if known.
func (s *Service) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	s.m.Lock()
	defer s.m.Unlock()
	locs := s.root[name]
	keys := s.keystore[name]
	return locs, keys, nil
}

// SetPublicKeys sets a slice of public keys to the keystore for a
// given user name. Previously-known keys for that user are
// forgotten. To add keys to the existing set, Lookup and append to
// the slice. If keys is nil, the user is forgotten.
func (s *Service) SetPublicKeys(name upspin.UserName, keys []upspin.PublicKey) {
	s.m.Lock()
	defer s.m.Unlock()
	if keys == nil {
		delete(s.keystore, name)
	} else {
		s.keystore[name] = keys
	}
}

// ListUsers returns a slice of all known users with at least one public key.
func (s *Service) ListUsers() []upspin.UserName {
	s.m.Lock()
	defer s.m.Unlock()
	users := make([]upspin.UserName, 0, len(s.keystore))
	for u := range s.keystore {
		users = append(users, u)
	}
	return users
}

// Install installs a user and its root in the provided Directory
// service. For a real User service, this would be done by some offline
// adminstrative procedure. For this test version, we just provide a
// simple hook for testing.
func (s *Service) Install(name upspin.UserName, dir upspin.Directory) error {
	s.m.Lock()
	defer s.m.Unlock()
	// Verify that it is a valid name. First make it look like a directory by adding a slash.
	parsed, err := path.Parse(upspin.PathName(name + "/"))
	if err != nil {
		return err
	}
	if len(parsed.Elems) != 0 {
		return fmt.Errorf("testuser: %q not a user name", name)
	}
	// _, ok := s.root[name]
	// if ok {
	// 	return fmt.Errorf("testuser: user %q already installed", name)
	// }

	loc, err := dir.MakeDirectory(upspin.PathName(parsed.User + "/"))
	if err != nil {
		return err
	}
	s.root[name] = []upspin.Endpoint{loc.Endpoint}
	return nil
}

// Methods to implement upspin.Access

func (s *Service) ServerUserName() string {
	return "testuser"
}

// Dial always returns the same instance of the service. The Transport must be InProcess
// but the NetAddr is ignored.
func (s *Service) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("testuser: unrecognized transport")
	}
	return s, nil
}

func init() {
	s := &Service{
		root:     make(map[upspin.UserName][]upspin.Endpoint),
		keystore: make(map[upspin.UserName][]upspin.PublicKey),
	}
	access.RegisterUser(upspin.InProcess, s)
}
