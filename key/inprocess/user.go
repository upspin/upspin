// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inprocess implements a non-persistent, memory-resident user service.
package inprocess

import (
	"sync"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
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
	mu       sync.RWMutex
	root     map[upspin.UserName][]upspin.Endpoint
	keystore map[upspin.UserName]upspin.PublicKey
}

var _ upspin.KeyServer = (*Service)(nil)

var db *database

// Lookup reports the set of locations the user's directory might be,
// with the earlier entries being the best choice; later entries are
// fallbacks and the user's public keys, if known.
func (s *Service) Lookup(name upspin.UserName) (*upspin.User, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	// Return copies so the caller can't modify our data structures.

	u := &upspin.User{
		Name:      name,
		Dirs:      append([]upspin.Endpoint{}, db.root[name]...),
		PublicKey: db.keystore[name],
		Stores:    []upspin.Endpoint{},
	}
	return u, nil
}

// Put implements upspin.KeyServer.
func (s *Service) Put(user *upspin.User) error {
	return errors.E("Put", errors.Syntax, errors.Str("not implemented"))
}

// SetPublicKeys sets a slice of public keys to the keystore for a
// given user name. Previously-known keys for that user are
// forgotten. To add keys to the existing set, Lookup and append to
// the slice. If keys is nil, the user is forgotten.
// TODO: remove and use Put everywhere instead.
func (s *Service) SetPublicKeys(name upspin.UserName, keys []upspin.PublicKey) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if keys == nil {
		delete(db.keystore, name)
	} else {
		db.keystore[name] = keys[0]
	}
}

// ListUsers returns a slice of all known users with at least one public key.
func (s *Service) ListUsers() []upspin.UserName {
	db.mu.RLock()
	defer db.mu.RUnlock()
	users := make([]upspin.UserName, 0, len(db.keystore))
	for u := range db.keystore {
		users = append(users, u)
	}
	return users
}

// validateUserName returns a parsed path if the username is valid.
func validateUserName(op string, name upspin.UserName) (*path.Parsed, error) {
	parsed, err := path.Parse(upspin.PathName(name))
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !parsed.IsRoot() {
		return nil, errors.E(op, errors.Invalid, name, errors.Str("not a valid user name"))
	}
	return &parsed, nil
}

// Install installs a user and its.db.root in the provided DirServer.
// For a real KeyServer, this would be done by some offline
// administrative procedure. For this test version, we just provide a
// simple hook for testing.
func (s *Service) Install(name upspin.UserName, dir upspin.DirServer) error {
	// Verify that it is a valid name.
	parsed, err := validateUserName("Install", name)
	if err != nil {
		return err
	}
	entry, err := dir.MakeDirectory(upspin.PathName(parsed.User() + "/"))
	if err != nil {
		return err
	}
	if len(entry.Blocks) == 0 {
		return errors.E("Install", name, errors.Str("Directory has no location"))
	}
	s.addRoot(parsed.User(), entry.Blocks[0].Location.Endpoint)
	return nil
}

// addRoot adds a root for the user, which must be a parsed, valid Upspin user name.
func (s *Service) addRoot(userName upspin.UserName, endpoint upspin.Endpoint) {
	db.mu.Lock()
	db.root[userName] = append(db.root[userName], endpoint)
	db.mu.Unlock()
}

// AddRoot adds an endpoint as the user's.db.root endpoint.
// TODO: remove and use Put everywhere instead.
func (s *Service) AddRoot(name upspin.UserName, endpoint upspin.Endpoint) error {
	// Verify that it is a valid name.
	parsed, err := validateUserName("AddRoot", name)
	if err != nil {
		return err
	}
	s.addRoot(parsed.User(), endpoint)
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
		root:     make(map[upspin.UserName][]upspin.Endpoint),
		keystore: make(map[upspin.UserName]upspin.PublicKey),
	}

	bind.RegisterKeyServer(upspin.InProcess, &Service{})
}
