// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): rename this file to key.go and the test to key_test.go

// Package inprocess implements a non-persistent, memory-resident user service.
package inprocess

import (
	"sync"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/valid"
)

func New() upspin.KeyServer {
	return &server{db: &database{
		users: make(map[upspin.UserName]*upspin.User),
	}}
}

// server maps user names to potential machines holding root of the user's tree.
// There is one for each Dial call, but they all share the underlying database.
// It implements the upspin.KeyServer interface.
type server struct {
	upspin.NoConfiguration

	db *database
}

var _ upspin.KeyServer = (*server)(nil)

// A database holds the information for the known users.
// There is one instance, created in init, shared by all server objects.
type database struct {
	// mu protects the fields below.
	mu    sync.RWMutex
	users map[upspin.UserName]*upspin.User
}

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are
// fallbacks and the user's public keys, if known.
func (s *server) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "Lookup"
	if err := valid.UserName(name); err != nil {
		return nil, errors.E(op, err)
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	user, ok := s.db.users[name]
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
func (s *server) Put(user *upspin.User) error {
	const op = "Put"
	if err := valid.User(user); err != nil {
		return errors.E(op, err)
	}
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	s.db.users[user.Name] = dup(user)
	return nil
}

// Endpoint implements upspin.server.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
}

// Dial always returns the same instance of the service. The Transport must be InProcess
// but the NetAddr is ignored.
func (s *server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.InProcess {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}
	return s, nil
}

// Ping implements upspin.server.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.server.
func (s *server) Close() {
}

// Authenticate implements upspin.server.
func (s *server) Authenticate(upspin.Context) error {
	return nil
}

func init() {
	bind.RegisterKeyServer(upspin.InProcess, New())
}
