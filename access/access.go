// Package access contains the global AccessSwitch and its methods.
package access

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

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
func (as *accessSwitch) BindUser(cc upspin.ClientContext, loc upspin.Location) (upspin.User, error) {
	u, ok := as.user[loc.AccessName]
	if !ok {
		return nil, fmt.Errorf("User interface not registered: %s", loc.AccessName)
	}
	x, err := u.Dial(cc, loc)
	return x.(upspin.User), err
}

// BindStore implements upspin.AccessSwitch.BindStore
func (as *accessSwitch) BindStore(cc upspin.ClientContext, loc upspin.Location) (upspin.Store, error) {
	s, ok := as.store[loc.AccessName]
	if !ok {
		return nil, fmt.Errorf("Store interface not registered: %s", loc.AccessName)
	}
	x, err := s.Dial(cc, loc)
	return x.(upspin.Store), err
}

// BindDirectory implements upspin.AccessSwitch.BindDirectory
func (as *accessSwitch) BindDirectory(cc upspin.ClientContext, loc upspin.Location) (upspin.Directory, error) {
	d, ok := as.directory[loc.AccessName]
	if !ok {
		return nil, fmt.Errorf("Directory interface not registered: %s", loc.AccessName)
	}
	x, err := d.Dial(cc, loc)
	return x.(upspin.Directory), err
}

func init() {
	Switch = &accessSwitch{user: make(map[string]upspin.User),
		store:     make(map[string]upspin.Store),
		directory: make(map[string]upspin.Directory)}
}
