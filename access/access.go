// Package access contains the global AccessSwitch and its methods.
package access

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Switch is the global accessor for services.
var Switch upspin.AccessSwitch

type accessSwitch struct {
	user      map[upspin.Transport]upspin.User
	directory map[upspin.Transport]upspin.Directory
	store     map[upspin.Transport]upspin.Store
}

// RegisterUser implements upspin.AccessSwitch.RegisterUser
func (as *accessSwitch) RegisterUser(transport upspin.Transport, user upspin.User) error {
	_, ok := as.user[transport]
	if ok {
		return fmt.Errorf("cannot override User interface: %v", transport)
	}
	as.user[transport] = user
	return nil
}

// RegisterDirectory implements upspin.AccessSwitch.RegisterDirectory
func (as *accessSwitch) RegisterDirectory(transport upspin.Transport, dir upspin.Directory) error {
	_, ok := as.directory[transport]
	if ok {
		return fmt.Errorf("cannot override Directory interface: %v", transport)
	}
	as.directory[transport] = dir
	return nil
}

// RegisterStore implements upspin.AccessSwitch.RegisterStore
func (as *accessSwitch) RegisterStore(transport upspin.Transport, store upspin.Store) error {
	_, ok := as.store[transport]
	if ok {
		return fmt.Errorf("cannot override Store interface: %v", transport)
	}
	as.store[transport] = store
	return nil
}

// BindUser implements upspin.AccessSwitch.BindUser
func (as *accessSwitch) BindUser(cc upspin.ClientContext, e upspin.Endpoint) (upspin.User, error) {
	u, ok := as.user[e.Transport]
	if !ok {
		return nil, fmt.Errorf("User service with transport %q not registered", e.Transport)
	}
	x, err := u.Dial(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.User), nil
}

// BindStore implements upspin.AccessSwitch.BindStore
func (as *accessSwitch) BindStore(cc upspin.ClientContext, e upspin.Endpoint) (upspin.Store, error) {
	s, ok := as.store[e.Transport]
	if !ok {
		return nil, fmt.Errorf("Store service with transport %q not registered", e.Transport)
	}
	x, err := s.Dial(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.Store), nil
}

// BindDirectory implements upspin.AccessSwitch.BindDirectory
func (as *accessSwitch) BindDirectory(cc upspin.ClientContext, e upspin.Endpoint) (upspin.Directory, error) {
	d, ok := as.directory[e.Transport]
	if !ok {
		return nil, fmt.Errorf("Directory service with transport %q not registered", e.Transport)
	}
	x, err := d.Dial(cc, e)
	if err != nil {
		return nil, err
	}
	return x.(upspin.Directory), nil
}

func init() {
	Switch = &accessSwitch{user: make(map[upspin.Transport]upspin.User),
		store:     make(map[upspin.Transport]upspin.Store),
		directory: make(map[upspin.Transport]upspin.Directory)}
}
