// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements the user service upspin.KeyServer
// that runs on the Google Cloud Platform (GCP).
package gcp

import (
	"encoding/json"
	"strings"
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"

	// We use GCS as the backing for our data.
	_ "upspin.io/cloud/storage/gcs"
)

// New initializes an instance of the key service.
// Required configuration options are listed at the package comments.
func New(options ...string) (upspin.KeyServer, error) {
	const op = "key/gcp.New"

	// All options are for the Storage layer.
	var storageOpts []storage.DialOpts
	for _, o := range options {
		storageOpts = append(storageOpts, storage.WithOptions(o))
	}

	s, err := storage.Dial("GCS", storageOpts...)
	if err != nil {
		return nil, errors.E(op, err)
	}
	log.Debug.Printf("Configured GCP user: %v", options)
	return &server{storage: s, refCount: &refCount{count: 1}}, nil
}

// server is the implementation of the KeyServer Service on GCP.
type server struct {
	storage storage.Storage
	*refCount

	// The name of the user accessing this server, set by Dial.
	user upspin.UserName
}

var _ upspin.KeyServer = (*server)(nil)

type refCount struct {
	sync.Mutex
	count int
}

// userEntry is the on-disk representation of upspin.User, further annotated with
// non-public information, such as whether the user is an admin.
type userEntry struct {
	User    upspin.User
	IsAdmin bool
}

// Lookup implements upspin.KeyServer.
func (s *server) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "key/gcp.Lookup"
	if err := valid.UserName(name); err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := s.fetchUserEntry(op, name)
	if err != nil {
		return nil, err
	}
	return &entry.User, nil
}

// Put implements upspin.KeyServer.
func (s *server) Put(u *upspin.User) error {
	const op = "key/gcp.Put"
	if s.user == "" {
		return errors.E(op, errors.Internal, errors.Str("not bound to user"))
	}
	if err := valid.User(u); err != nil {
		return errors.E(op, err)
	}
	if err := s.canPut(op, u.Name); err != nil {
		return err
	}

	// Retrieve info about the user we want to Put.
	isAdmin := false
	entry, err := s.fetchUserEntry(op, u.Name)
	switch {
	case errors.Match(errors.E(errors.NotExist), err):
		// OK; adding new user.
	case err != nil:
		return err
	default:
		// User exists.
		isAdmin = entry.IsAdmin
	}

	// Set IsAdmin to what it was before or false by default.
	return s.putUserEntry(op, &userEntry{User: *u, IsAdmin: isAdmin})
}

// canPut reports whether the current logged in user can Put the target user.
func (s *server) canPut(op string, target upspin.UserName) error {
	name, suffix, domain, err := user.Parse(target)
	if err != nil {
		return errors.E(op, err)
	}
	// Do not allow * wildcard in name.
	if name == "*" {
		return errors.E(op, errors.Invalid, target, errors.Str("user has wildcard '*' in name"))
	}
	// If the current user is the same as target, it can proceed.
	if s.user == target {
		return nil
	}
	// For suffixed users, if the current user is the canonical user for
	// target, let it proceed.
	if suffix != "" {
		index := strings.Index(name, "+")
		if index <= 0 {
			return errors.E(op, errors.Internal, target, errors.Str("suffixed user but no '+' found"))
		}
		name = name[:index]
		if s.user == upspin.UserName(name+"@"+domain) {
			// Current user is the owner of target.
			return nil
		}
	}
	// Finally, check whether the current user is an admin.
	entry, err := s.fetchUserEntry(op, s.user)
	if err != nil {
		return err
	}
	if !entry.IsAdmin {
		return errors.E(op, errors.Permission, s.user, errors.Str("not an administrator"))
	}
	// Is admin. Proceed.
	return nil
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (s *server) fetchUserEntry(op string, name upspin.UserName) (*userEntry, error) {
	log.Debug.Printf("%s: %s", op, name)
	b, err := s.storage.Download(string(name))
	if err != nil {
		log.Error.Printf("%s: error fetching %q: %v", op, name, err)
		return nil, errors.E(op, name, err)
	}
	var entry userEntry
	if err := json.Unmarshal(b, &entry); err != nil {
		return nil, errors.E(op, errors.Invalid, name, err)
	}
	return &entry, nil
}

// putUserEntry writes the user entry for a user to permanent storage on GCP.
func (s *server) putUserEntry(op string, entry *userEntry) error {
	log.Debug.Printf("%s: %s", op, entry.User.Name)
	if entry == nil {
		return errors.E(op, errors.Invalid, errors.Str("nil userEntry"))
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	if _, err := s.storage.Put(string(entry.User.Name), b); err != nil {
		return errors.E(op, errors.IO, err)
	}
	return nil
}

// Dial implements upspin.Service.
func (s *server) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	s.refCount.Lock()
	s.refCount.count++
	s.refCount.Unlock()

	svc := *s
	svc.user = ctx.UserName()
	return &svc, nil
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *server) Close() {
	// This instance is no longer tied to a user.
	s.user = ""

	s.refCount.Lock()
	defer s.refCount.Unlock()
	s.refCount.count--

	if s.refCount.count == 0 {
		if s.storage != nil {
			s.storage.Close()
		}
		s.storage = nil
	}
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
