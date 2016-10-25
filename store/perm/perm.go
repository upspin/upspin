// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package perm implements mutation permission checking for StoreServer
// implementations.
package perm

// Features:
// - Resolves remote Group files if necessary.
// - Blocks mutations to Store until it has had a chance to prove that either
//   there is no Group file and hence writes are free for all, or until the
//   Group file has been fully loaded. This prevents a window of vulnerability
//   where all writes would be allowed until the initial load is completed.
//
// TODOs:
// - Cache references so we don't need to retrieve the contents every time.
// - Poll more frequently if there is no control Group set up, so the StoreServer
//   updates faster when creating a new one for the first time.
// - Poll more frequently if the DirServer is unreachable (speeds up boot time).

import (
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	// StoreWritersGroupFile is the name of the Group file relative to this
	// StoreServer's users's Group that dictates which users can write or
	// mutate the store.
	StoreWritersGroupFile = "StoreWriters"

	// pollInterval is how often to poll for updates to the permission file.
	pollInterval = 2 * time.Minute

	// retryTimeout is the interval between re-attempts between failures.
	retryTimeout = 10 * time.Second
)

// Store performs permission checking for StoreServer implementations.
type Store struct {
	upspin.StoreServer

	serverCtx upspin.Context

	// HACK: Indirection here is to avoid copying mutex and waitgroup.
	writers *storeWriters // writers to this Store.
}

// storeWriters stores all users allowed to mutate the store.
type storeWriters struct {
	// firstRun ensures no mutations can go through until we have resolved
	// mutation permission checking for the first time.
	firstRun sync.WaitGroup

	mu sync.Mutex // protects the map.
	w  map[upspin.UserName]bool
}

// WrapStore creates a new permission check for the StoreServer in the context.
func WrapStore(ctx upspin.Context, store upspin.StoreServer) *Store {
	s := &Store{
		StoreServer: store,
		serverCtx:   ctx,
		writers: &storeWriters{
			w: make(map[upspin.UserName]bool),
		},
	}
	s.writers.firstRun.Add(1)
	go s.updateLoop()
	return s
}

// Put implements upspin.StoreServer.
func (s *Store) Put(data []byte) (*upspin.Refdata, error) {
	const op = "store/perm.Put"

	if !s.IsWriter(s.serverCtx.UserName()) {
		return nil, errors.E(op, s.serverCtx.UserName(), errors.Permission, errors.Errorf("user not authorized"))
	}
	return s.StoreServer.Put(data)
}

// Delete implements upspin.StoreServer.
func (s *Store) Delete(ref upspin.Reference) error {
	const op = "store/perm.Delete"

	if !s.IsWriter(s.serverCtx.UserName()) {
		return errors.E(op, s.serverCtx.UserName(), errors.Permission, errors.Errorf("user not authorized"))
	}
	return s.StoreServer.Delete(ref)
}

// Dial implements upspin.Service.
func (s *Store) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/perm.Dial"
	service, err := s.StoreServer.Dial(context, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newS := *s
	newS.serverCtx = context
	newS.StoreServer = service.(upspin.StoreServer)
	return &newS, nil
}

// updateLoop continuously looks for updates on this StoreServer's permissions.
// It must be run in a goroutine before calling IsMutationAllowed.
func (s *Store) updateLoop() {
	err := s.UpdateNow()
	if err != nil {
		log.Error.Printf("Error updating StoreServer's writers: %s", err)
	}
	s.writers.firstRun.Done()

	for {
		err = s.UpdateNow()
		if err != nil {
			log.Error.Printf("Error updating StoreServer's writers: %s", err)
			time.Sleep(retryTimeout)
			continue
		}
		time.Sleep(pollInterval)
	}
}

// UpdateNow retrieves and parses the Group file that rules over this
// StoreServer's allowed writers.
func (s *Store) UpdateNow() error {
	entry, err := s.lookupGroupFile()
	if err != nil {
		// If the group file does not exist, reset writers map.
		if errors.Match(errors.E(errors.NotExist), err) {
			s.deleteAllWriters()
			return nil
		}
		return err
	}
	users, err := s.allowedWriters(entry)
	if err != nil {
		return err
	}
	// Atomically update the contents of p.allowed.
	s.writers.mu.Lock()
	defer s.writers.mu.Unlock()
	s.deleteAllWritersLocked()
	for _, u := range users {
		s.writers.w[u] = true
	}
	return nil
}

// lookupGroupFile looks up the Group file that rules over this StoreServer.
func (s *Store) lookupGroupFile() (*upspin.DirEntry, error) {
	return s.lookup(upspin.PathName(string(s.serverCtx.UserName()) + "/Group/" + StoreWritersGroupFile))
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (s *Store) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can easily use it to retrieve a
	// slice of  all authorized users.
	fakeAccess := "w,d:" + entry.Name
	acc, err := access.Parse(upspin.PathName(s.serverCtx.UserName())+"/", []byte(fakeAccess))
	if err != nil {
		return nil, err
	}

	return acc.Users(access.Write, s.load)
}

// load loads the contents of a name from the StoreServer.
// Intended for use with access.Users.
func (s *Store) load(name upspin.PathName) ([]byte, error) {
	entry, err := s.lookup(name)
	if err != nil {
		return nil, err
	}
	// TODO: use an entry cache here.

	return clientutil.ReadAll(s.serverCtx, entry)
}

// lookup performs a directory entry lookup on the canonical DirServer for
// the path.
func (s *Store) lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	key, err := bind.KeyServer(s.serverCtx, s.serverCtx.KeyEndpoint())
	if err != nil {
		return nil, err
	}
	u, err := key.Lookup(parsed.User())
	if err != nil {
		return nil, err
	}
	var firstErr error
	check := func(err error) error {
		if firstErr == nil {
			firstErr = err
		}
		return err
	}
	for _, e := range u.Dirs {
		dir, err := bind.DirServer(s.serverCtx, e)
		if check(err) != nil {
			// Skip bad bind.
			continue
		}
		return dir.Lookup(parsed.Path())
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.E(errors.NotExist, parsed.Path(), errors.Str("no dir entry for path"))
}

// IsWriter reports whether the user has mutation privileges on the
// StoreServer. It is intended to be called from StoreServer.Put and
// StoreServer.Delete.
func (s *Store) IsWriter(u upspin.UserName) bool {
	s.writers.firstRun.Wait()
	s.writers.mu.Lock()
	defer s.writers.mu.Unlock()
	// Everyone is allowed if there is no control Group yet.
	if len(s.writers.w) == 0 {
		return true
	}
	// If the special user "all@upspin.io" is present, allow all.
	if s.writers.w[access.All] {
		return true
	}
	// Is this exact user allowed?
	if s.writers.w[u] {
		return true
	}
	// Maybe the domain is wildcarded. Check this case last as it's the most
	// expensive.
	_, _, domain, err := user.Parse(u)
	if err != nil {
		// Should never happen at this point.
		log.Error.Printf("store/perm: unexpected error: %s", err)
		return false
	}
	return s.writers.w[upspin.UserName("*@"+domain)]
}

func (s *Store) deleteAllWriters() {
	s.writers.mu.Lock()
	s.deleteAllWritersLocked()
	s.writers.mu.Unlock()
}

func (s *Store) deleteAllWritersLocked() {
	for w := range s.writers.w {
		delete(s.writers.w, w)
	}
}
