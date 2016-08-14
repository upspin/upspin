// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a non-persistent, memory-resident user service.
package inprocess

import (
	"sync"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/valid"
)

// Service maps user names to potential machines holding root of the user's tree.
// There is one for each Dial call, but they all share the underlying database.
// It implements the upspin.KeyServer interface.
type Service struct {
	upspin.NoConfiguration
	// context holds the context that created the call.
	context upspin.Context
}

// A database holds the information for the known users.
// There is one instance, created in init, shared by all Service objects.
type database struct {
	// mu protects the fields below.
	mu    sync.RWMutex
	users map[upspin.UserName]*upspin.User
}

var _ upspin.KeyServer = (*Service)(nil)

var db *database

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are
// fallbacks and the user's public keys, if known.
func (s *Service) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "Lookup"
	if err := valid.UserName(name); err != nil {
		return nil, errors.E(op, err)
	}

	db.mu.RLock()
	defer db.mu.RUnlock()
	user, ok := db.users[name]
	if !ok {
		return nil, errors.E(op, name, errors.NotExist)
	}

	return dup(user), nil
}

// dup creates a copy of the User structure so the caller cannot change our data structures.
func dup(u *upspin.User) *upspin.User {
	v := *u
	// The slices need to be copied.
	v.Dirs = make([]upspin.Endpoint, len(u.Dirs))
	copy(v.Dirs, u.Dirs)
	v.Stores = make([]upspin.Endpoint, len(u.Stores))
	copy(v.Stores, u.Stores)
	return &v
}

// Put implements upspin.KeyServer.
func (s *Service) Put(user *upspin.User) error {
	const op = "Put"
	if err := valid.User(user); err != nil {
		return errors.E(op, err)
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	db.users[user.Name] = dup(user)
	return nil
}

// Endpoint implements upspin.Service.
func (s *Service) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
}

// Dial always returns the same instance of the service. The Transport must be InProcess
// but the NetAddr is ignored.
func (s *Service) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	return &Service{
		context: context.Copy(),
	}, nil
}

// Ping implements upspin.Service.
func (s *Service) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *Service) Close() {
}

// Authenticate implements upspin.Service.
func (s *Service) Authenticate(upspin.Context) error {
	return nil
}

func init() {
	db = &database{
		users: make(map[upspin.UserName]*upspin.User),
	}

	bind.RegisterKeyServer(upspin.InProcess, &Service{})
}
