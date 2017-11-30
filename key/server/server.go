// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server implements the user service upspin.KeyServer
// that runs with the backing of storage.Storage.
package server

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"sync"

	"upspin.io/cache"
	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"
)

const cacheSize = 10000

// New initializes an instance of the KeyServer
// that stores its data in the given Storage implementation.
func New(options ...string) (upspin.KeyServer, error) {
	const op errors.Op = "key/server.New"

	var backend string
	var dialOpts []storage.DialOpts
	for _, option := range options {
		const prefix = "backend="
		if strings.HasPrefix(option, prefix) {
			backend = option[len(prefix):]
			continue
		}
		// Pass other options to the storage backend.
		dialOpts = append(dialOpts, storage.WithOptions(option))
	}
	if backend == "" {
		return nil, errors.E(op, errors.Invalid, `storage "backend" option is missing`)
	}
	s, err := storage.Dial(backend, dialOpts...)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return &server{
		storage:   s,
		refCount:  &refCount{count: 1},
		lookupTXT: net.LookupTXT,
		logger:    &loggerImpl{storage: s},
		cache:     cache.NewLRU(cacheSize),
		negCache:  cache.NewLRU(cacheSize),
	}, nil
}

// server is the implementation of the KeyServer Service.
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

	// cache caches known users. Key is a UserName. Value is *userEntry.
	cache *cache.LRU

	// negCache caches the absence of a user. Key is a UserName and value is
	// ignored.
	negCache *cache.LRU
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
	const op errors.Op = "key/server.Lookup"
	m, span := metric.NewSpan(op)
	defer m.Done()

	if err := valid.UserName(name); err != nil {
		return nil, errors.E(op, name, err)
	}
	entry, err := s.lookup(op, name, span)
	if err != nil {
		return nil, err
	}
	return &entry.User, nil
}

// lookup looks up the internal user record, using caches when available.
func (s *server) lookup(op errors.Op, name upspin.UserName, span *metric.Span) (*userEntry, error) {
	// Check positive cache first.
	if entry, found := s.cache.Get(name); found {
		return entry.(*userEntry), nil
	}
	// Check negative cache next.
	if _, found := s.negCache.Get(name); found {
		return nil, errors.E(errors.NotExist, name)
	}

	// No information. Find it on our storage backend.
	sp := span.StartSpan(op + ": fetchUserEntry")
	entry, err := s.fetchUserEntry(op, name)
	sp.End()
	if err != nil {
		// Not found: add to negative cache.
		if errors.Is(errors.NotExist, err) {
			s.negCache.Add(name, true)
		}
		return nil, err
	}

	// Found. Add to positive cache.
	s.cache.Add(name, entry)

	return entry, nil
}

// Put implements upspin.KeyServer.
func (s *server) Put(u *upspin.User) error {
	const op errors.Op = "key/server.Put"
	m, span := metric.NewSpan(op)
	defer m.Done()

	if s.user == "" {
		return errors.E(op, errors.Internal, "not bound to user")
	}
	if err := valid.User(u); err != nil {
		return errors.E(op, err)
	}

	// Retrieve info about the user we want to Put.
	isAdmin := false
	newUser := false

	entry, err := s.lookup(op, u.Name, span)
	switch {
	case errors.Is(errors.NotExist, err):
		// OK; adding new user.
		newUser = true
	case err != nil:
		return err
	default:
		// User exists.
		isAdmin = entry.IsAdmin
	}

	if err := s.canPut(op, u.Name, newUser, span); err != nil {
		return err
	}

	sp := span.StartSpan("logger.PutAttempt")
	err = s.logger.PutAttempt(s.user, u)
	sp.End()
	if err != nil {
		return errors.E(op, err)
	}

	entry = &userEntry{User: *u, IsAdmin: isAdmin}
	sp = span.StartSpan("putUserEntry")
	err = s.putUserEntry(op, entry)
	sp.End()
	if err != nil {
		// Clear out both negative and positive caches on error since we are not certain
		// about the remote storage state.
		s.negCache.Remove(u.Name)
		s.cache.Remove(u.Name)
		return err
	}

	// Remove this new user from the negative cache and update it on the
	// positive cache.
	s.negCache.Remove(u.Name)
	s.cache.Add(u.Name, entry)

	sp = span.StartSpan("logger.PutSuccess")
	err = s.logger.PutSuccess(s.user, u)
	sp.End()
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

// canPut reports whether the current logged-in user can Put the (new or
// existing) target user.
func (s *server) canPut(op errors.Op, target upspin.UserName, isTargetNew bool, span *metric.Span) error {
	sp := span.StartSpan("canPut")
	defer sp.End()

	name, suffix, domain, err := user.Parse(target)
	if err != nil {
		return errors.E(op, err)
	}
	// Do not allow * wildcard in name.
	if name == "*" {
		return errors.E(op, errors.Invalid, target, "user has wildcard '*' in name")
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
	ss := sp.StartSpan("fetchUserEntry")
	entry, err := s.fetchUserEntry(op, s.user)
	ss.End()
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
	err = s.verifyOwns(s.user, entry.User.PublicKey, domain)
	if err == nil {
		// User owns the domain for the target user.
		return nil
	}
	return errors.E(op, errors.Permission, s.user, err)
}

// fetchUserEntry reads the user entry for a given user from the storage.
func (s *server) fetchUserEntry(op errors.Op, name upspin.UserName) (*userEntry, error) {
	b, err := s.storage.Download(string(name))
	if err != nil {
		return nil, errors.E(op, name, err)
	}
	var entry userEntry
	if err = json.Unmarshal(b, &entry); err != nil {
		return nil, errors.E(op, errors.Invalid, name, err)
	}
	return &entry, nil
}

// putUserEntry writes the user entry for a user to the storage.
func (s *server) putUserEntry(op errors.Op, entry *userEntry) error {
	if entry == nil {
		return errors.E(op, errors.Invalid, "nil userEntry")
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	if err = s.storage.Put(string(entry.User.Name), b); err != nil {
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
	lastErr := errors.Errorf("not an administrator for %s", domain)
	const prefix = "upspin:"
	for _, txt := range txts {
		if len(txt) < len(prefix)+20 {
			// not enough data.
			continue
		}
		if !strings.HasPrefix(txt, prefix) {
			continue
		}
		txt = txt[len(prefix):]
		// Is there a signature with two segments after the prefix?
		sigFields := strings.Split(txt, "-")
		if len(sigFields) != 2 {
			continue
		}
		// Parse signature.
		var sig upspin.Signature
		var rs, ss big.Int
		if _, ok := rs.SetString(sigFields[0], 16); !ok {
			lastErr = errors.E(errors.Invalid, "invalid signature field0")
			continue
		}
		if _, ok := ss.SetString(sigFields[1], 16); !ok {
			lastErr = errors.E(errors.Invalid, "invalid signature field1")
			continue
		}
		sig.R = &rs
		sig.S = &ss

		hash := sha256.Sum256([]byte("upspin-domain:" + domain + "-" + string(u)))
		err := factotum.Verify(hash[:], sig, pubKey)
		if err == nil {
			// Success!
			return nil
		}
		log.Debug.Printf("key/server: failed to verify that %q owns %q with pubKey %q, sig %q: %v", u, domain, pubKey, txt, err)
		lastErr = errors.E(errors.Errorf("%s: verifying ownership of domain %s; re-run cmd/upspin setupdomain?", err, domain))
	}
	return lastErr
}

// Log implements Logger.
func (s *server) Log() ([]byte, error) {
	const op errors.Op = "key/server.Log"

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

// Close implements upspin.Service.
func (s *server) Close() {
	// This instance is no longer tied to a user.
	s.user = ""

	s.refCount.Lock()
	defer s.refCount.Unlock()
	s.refCount.count--

	if s.refCount.count == 0 {
		s.storage = nil
	}
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
