package user

import (
	"errors"

	"upspin.googlesource.com/upspin.git/sim/path"
	"upspin.googlesource.com/upspin.git/sim/ref"
)

// Service maps user names to references to the root of the user's tree.
type Service struct {
	root map[path.UserName][]ref.Location
}

// UserLookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are fallbacks.
func (s *Service) Lookup(name path.UserName) ([]ref.Location, error) {
	locs, ok := s.root[name]
	if !ok {
		return nil, errors.New("no such user")
	}
	return locs, nil
}
