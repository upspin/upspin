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
// - Use it in store/gcp (next CL).
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
	serverCtx upspin.Context

	// firstRun ensures no mutations can go through until we have resolved
	// mutation permission checking for the first time.
	firstRun sync.WaitGroup

	mu sync.Mutex // protects fields below.
	// writers stores all users allowed to mutate the store.
	writers map[upspin.UserName]bool
}

// NewStore creates a new permission check for the StoreServer in the context.
func NewStore(ctx upspin.Context) *Store {
	p := &Store{
		serverCtx: ctx,
	}
	p.firstRun.Add(1)
	return p
}

// UpdateLoop continuously looks for updates on this StoreServer's permissions.
// It must be run in a goroutine before calling IsMutationAllowed.
func (p *Store) UpdateLoop() {
	err := p.updateAllowedWriters()
	if err != nil {
		log.Error.Printf("Error updating StoreServer's writers: %s", err)
	}
	p.firstRun.Done()

	for {
		err = p.updateAllowedWriters()
		if err != nil {
			log.Error.Printf("Error updating StoreServer's writers: %s", err)
			time.Sleep(retryTimeout)
			continue
		}
		time.Sleep(pollInterval)
	}
}

// updateAllowedWriters retrieves and parses the Group file that rules over
// this StoreServer's allowed writers.
func (p *Store) updateAllowedWriters() error {
	entry, err := p.lookupGroupFile()
	if err != nil {
		return err
	}
	users, err := p.allowedWriters(entry)
	if err != nil {
		return err
	}
	// Atomically update the contents of p.allowed.
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = make(map[upspin.UserName]bool)
	for _, u := range users {
		p.writers[u] = true
	}
	return nil
}

// lookupGroupFile looks up the Group file that rules over this StoreServer.
func (p *Store) lookupGroupFile() (*upspin.DirEntry, error) {
	return p.lookup(upspin.PathName(string(p.serverCtx.UserName()) + "/Group/" + StoreWritersGroupFile))
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (p *Store) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can easily use it to retrieve a
	// slice of  all authorized users.
	fakeAccess := "w,d:" + entry.Name
	acc, err := access.Parse(upspin.PathName(p.serverCtx.UserName())+"/", []byte(fakeAccess))
	if err != nil {
		return nil, err
	}

	return acc.Users(access.Write, p.load)
}

// load loads the contents of a name from the StoreServer.
// Intended for use with access.Users.
func (p *Store) load(name upspin.PathName) ([]byte, error) {
	entry, err := p.lookup(name)
	if err != nil {
		return nil, err
	}
	// TODO: use an entry cache here.

	return clientutil.ReadAll(p.serverCtx, entry)
}

// lookup performs a directory entry lookup on the canonical DirServer for
// the path.
func (p *Store) lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	key, err := bind.KeyServer(p.serverCtx, p.serverCtx.KeyEndpoint())
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
		dir, err := bind.DirServer(p.serverCtx, e)
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

// IsAllowedMutation reports whether the user has mutation privileges on the
// StoreServer. It is intended to be called from StoreServer.Put and
// StoreServer.Delete.
func (p *Store) IsAllowedMutation(u upspin.UserName) bool {
	p.firstRun.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	// Everyone is allowed if there are no control Groups yet.
	if p.writers == nil {
		return true
	}
	// If the special user "all@upspin.io" is present, allow all.
	if p.writers[access.All] {
		return true
	}
	return p.writers[u] // If u is not found, false is returned (default for bool).
}
