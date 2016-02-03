// Package access contains the global AccessSwitch and its methods.
package access

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Switch is the global accessor for services.
var Switch upspin.AccessSwitch

type accessSwitch struct {
	user      map[string]upspin.User
	directory map[string]upspin.Directory
	store     map[string]upspin.Store
}

// RegisterUser implements upspin.AccessSwitch.RegisterUser
func (as *accessSwitch) RegisterUser(name string, user upspin.User) error {
	_, ok := as.user[name]
	if ok {
		return fmt.Errorf("cannot override User interface: %s", name)
	}
	as.user[name] = user
	return nil
}

// RegisterDirectory implements upspin.AccessSwitch.RegisterDirectory
func (as *accessSwitch) RegisterDirectory(name string, dir upspin.Directory) error {
	_, ok := as.directory[name]
	if ok {
		return fmt.Errorf("cannot override Directory interface: %s", name)
	}
	as.directory[name] = dir
	return nil
}

// RegisterStore implements upspin.AccessSwitch.RegisterStore
func (as *accessSwitch) RegisterStore(name string, store upspin.Store) error {
	_, ok := as.store[name]
	if ok {
		return fmt.Errorf("cannot override Store interface: %s", name)
	}
	as.store[name] = store
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
	Switch = &accessSwitch{user: make(map[string]upspin.User),
		store:     make(map[string]upspin.Store),
		directory: make(map[string]upspin.Directory)}
}
