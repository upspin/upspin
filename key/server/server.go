// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements the user service upspin.KeyServer
// that runs with the backing of storage.Storage.
package server

import (
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"

	// We use GCS as the backing for our data.
	_ "upspin.io/cloud/storage/gcs"
	"upspin.io/metric"
)

// New initializes an instance of the key service.
// Required configuration options are listed at the package comments.
func New(options ...string) (upspin.KeyServer, error) {
	const op = "key/server.New"

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
	return &server{
		storage:   s,
		refCount:  &refCount{count: 1},
		lookupTXT: net.LookupTXT,
		logger:    &loggerImpl{storage: s},
	}, nil
}

// server is the implementation of the KeyServer Service on GCP.
type server struct {
	storage storage.Storage
	*refCount

	// A text log of all mutations to the key server.
	logger

	// The name of the user accessing this server, set by Dial.
	user upspin.UserName

	// lookupTXT is a DNS lookup function that returns all TXT fields for a
	// domain. It should alias net.LookupTXT except in tests.
	lookupTXT func(domain string) ([]string, error)
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
	const op = "key/server.Lookup"
	m, span := metric.NewSpan(op)
	defer m.Done()

	sp := span.StartSpan("validateName")
	if err := valid.UserName(name); err != nil {
		return nil, errors.E(op, name, err)
	}
	sp.End()
	sp = span.StartSpan("fetchUserEntry")
	defer sp.End()
	entry, err := s.fetchUserEntry(op, name, sp)
	if err != nil {
		return nil, err
	}
	return &entry.User, nil
}

// Put implements upspin.KeyServer.
func (s *server) Put(u *upspin.User) error {
	const op = "key/server.Put"
	m, span := metric.NewSpan(op)
	defer m.Done()

	sp := span.StartSpan("valid.User")
	if s.user == "" {
		return errors.E(op, errors.Internal, errors.Str("not bound to user"))
	}
	if err := valid.User(u); err != nil {
		return errors.E(op, err)
	}
	sp.End()
	sp = span.StartSpan("fetchUserEntry")

	// Retrieve info about the user we want to Put.
	isAdmin := false
	newUser := false
	entry, err := s.fetchUserEntry(op, u.Name, span)
	sp.End()
	switch {
	case errors.Match(errors.E(errors.NotExist), err):
		// OK; adding new user.
		newUser = true
	case err != nil:
		return err
	default:
		// User exists.
		isAdmin = entry.IsAdmin
	}
	sp = span.StartSpan("canPut")
	if err := s.canPut(op, u.Name, newUser, sp); err != nil {
		return err
	}

	if err := s.logger.PutAttempt(s.user, u); err != nil {
		return errors.E(op, err)
	}

	err = s.putUserEntry(op, &userEntry{User: *u, IsAdmin: isAdmin})
	if err != nil {
		return err
	}

	if err := s.logger.PutSuccess(s.user, u); err != nil {
		return errors.E(op, err)
	}

	return nil
}

// canPut reports whether the current logged-in user can Put the (new or
// existing) target user.
func (s *server) canPut(op string, target upspin.UserName, isTargetNew bool, span *metric.Span) error {
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
		uname := name[:len(name)-(len("+")+len(suffix))]
		if s.user == upspin.UserName(uname+"@"+domain) {
			// Current user is the owner of target.
			return nil
		}
	}
	// Check whether the current user is a global admin.
	entry, err := s.fetchUserEntry(op, s.user, span)
	if err != nil {
		return err
	}
	if entry.IsAdmin {
		return nil
	}
	// Finally, for a newly-created user, check whether the logged-in user
	// owns the domain (and is thus allowed to create new users).
	if !isTargetNew {
		// Even domain admins cannot update their users.
		return errors.E(op, errors.Exist, s.user)
	}
	sp := span.StartSpan("verifyOwns")
	err = s.verifyOwns(s.user, entry.User.PublicKey, domain)
	sp.End()
	if err == nil {
		// User owns the domain for the target user.
		return nil
	}
	return errors.E(op, errors.Permission, s.user, err)
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (s *server) fetchUserEntry(op string, name upspin.UserName, span *metric.Span) (*userEntry, error) {
	log.Debug.Printf("%s: %s", op, name)
	sp := span.StartSpan("storage.Download")
	b, err := s.storage.Download(string(name))
	sp.End()
	if err != nil {
		log.Error.Printf("%s: error fetching %q: %v", op, name, err)
		return nil, errors.E(op, name, err)
	}
	sp = span.StartSpan("json.Unmarshal")
	var entry userEntry
	err = json.Unmarshal(b, &entry)
	sp.End()
	if err != nil {
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

// verifyOwns verifies whether the named user owns the given domain name, as
// per the domain name's upspin TXT field signature.
func (s *server) verifyOwns(u upspin.UserName, pubKey upspin.PublicKey, domain string) error {
	txts, err := s.lookupTXT(domain)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	lastErr := errors.Str("not an administrator")
	const prefix = "upspin:"
	for _, txt := range txts {
		if len(txt) < len(prefix)+20 {
			// not enough data.
			continue
		}
		if !strings.HasPrefix(txt, prefix) {
			continue
		}
		// Is there a signature with two segments after the prefix?
		sigFields := strings.Split(txt[len(prefix):], "-")
		if len(sigFields) != 2 {
			continue
		}
		// Parse signature.
		var sig upspin.Signature
		var rs, ss big.Int
		if _, ok := rs.SetString(sigFields[0], 16); !ok {
			lastErr = errors.E(errors.Invalid, errors.Str("invalid signature field0"))
			continue
		}
		if _, ok := ss.SetString(sigFields[1], 16); !ok {
			lastErr = errors.E(errors.Invalid, errors.Str("invalid signature field1"))
			continue
		}
		sig.R = &rs
		sig.S = &ss

		log.Debug.Printf("Verifying if %q owns %q with pubKey: %q. Got sig: %q", u, domain, pubKey, txt[len(prefix):])
		msg := "upspin-domain:" + domain + "-" + string(u)
		lastErr = factotum.Verify([]byte(msg), sig, pubKey)
		if lastErr == nil {
			// Success!
			return nil
		}
	}
	return lastErr
}

// Log implements Logger.
func (s *server) Log() ([]byte, error) {
	const op = "key/server.Log"

	data, err := s.logger.ReadAll()
	if err != nil {
		return nil, errors.E(op, err)
	}
	return data, nil
}

// Dial implements upspin.Service.
func (s *server) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s.refCount.Lock()
	s.refCount.count++
	s.refCount.Unlock()

	svc := *s
	svc.user = cfg.UserName()
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
