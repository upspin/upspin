// Package testuser implements a non-persistent, memory-resident user service.
package tesetuser

import (
	"errors"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Service maps user names to potential machines holdining root of the user's tree.
// It implements the upspin.User interface.
type Service struct {
	root map[upspin.UserName][]upspin.Endpoint
}

var _ upspin.User = (*Service)(nil)

// UserLookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are fallbacks.
func (s *Service) Lookup(name upspin.UserName) ([]upspin.Endpoint, error) {
	locs, ok := s.root[name]
	if !ok {
		return nil, errors.New("no such user")
	}
	return locs, nil
}

// Methods to implement upspin.Access

func (s *Service) ServerUserName() string {
	return "testuser"
}

func (s *Service) Dial(context upspin.ClientContext, ep upspin.Endpoint) (interface{}, error) {
	return &Service{
		root: make(map[upspin.UserName][]upspin.Endpoint),
	}, nil
}

func init() {
	access.Switch.RegisterUser("in-process", &Service{})
}
