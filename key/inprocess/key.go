// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a non-persistent, memory-resident user service.
package inprocess // import "upspin.io/key/inprocess"

import (
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/user"
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
	const op errors.Op = "key/inprocess.Lookup"
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
func (s *server) Put(u *upspin.User) error {
	const op errors.Op = "key/inprocess.Put"
	if err := valid.User(u); err != nil {
		return errors.E(op, err)
	}
	name, _, _, err := user.Parse(u.Name)
	if err != nil {
		return errors.E(op, err)
	}
	if name == "*" {
		return errors.E(op, errors.Invalid, u.Name, "user has wildcard '*' in name")
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.users[u.Name] = dup(u)
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
func (s *server) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "key/inprocess.Dial"
	if e.Transport != upspin.InProcess {
		return nil, errors.E(op, errors.Invalid, "unrecognized transport")
	}
	return s, nil
}

// Close implements upspin.server.
func (s *server) Close() {
}
