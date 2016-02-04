// Package testuser implements a non-persistent, memory-resident user service.
package testuser

import (
	"errors"
	"fmt"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service maps user names to potential machines holdining root of the user's tree.
// It implements the upspin.User interface.
type Service struct {
	root map[upspin.UserName][]upspin.Endpoint
}

var _ upspin.User = (*Service)(nil)

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are fallbacks.
func (s *Service) Lookup(name upspin.UserName) ([]upspin.Endpoint, error) {
	locs, ok := s.root[name]
	if !ok {
		return nil, fmt.Errorf("testuser: no root for user %q", name)
	}
	return locs, nil
}

// Install installs a user and its root in the provided Directory
// service. For a real User service, this would be done by some offline
// adminstrative procedure. For this test version, we just provide a
// simple hook for testing.
func (s *Service) Install(name upspin.UserName, dir upspin.Directory) error {
	// Verify that it is a valid name. First make it look like a directory by adding a slash.
	parsed, err := path.Parse(upspin.PathName(name + "/"))
	if err != nil {
		return err
	}
	if len(parsed.Elems) != 0 {
		return fmt.Errorf("testuser: %q not a user name", name)
	}
	_, ok := s.root[name]
	if ok {
		return fmt.Errorf("testuser: user %q already installed", name)
	}
	loc, err := dir.MakeDirectory(upspin.PathName(parsed.User))
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
func (s *Service) Dial(context upspin.ClientContext, e upspin.Endpoint) (interface{}, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.New("testuser: unrecognized transport")
	}
	return s, nil
}

func init() {
	s := &Service{
		root: make(map[upspin.UserName][]upspin.Endpoint),
	}
	access.RegisterUser(upspin.InProcess, s)
}
