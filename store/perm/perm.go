// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package perm implements mutation permission checking for the Store.
package perm

// This file deals with mutation (put, delete) permissions on Store.
// Features:
// - Works with any store implementation.
// - Resolves remote Group files if necessary.
// - Blocks mutations to Store until it has had a chance to prove that either
//   there is no Group file and hence writes are free for all, or until the
//   Group file has been fully loaded. This prevents a window of vulnerability
//   where all writes would be allowed until the initial load is completed.
//
// TODOs:
// - Use it in store/gcp (next CL).
// - Cache references so we don't need to retrieve the contents every time.
// - Poll more frequently if there is no control Group set up, so the Store
//   updates faster when creating a new one for the first time.
// - Poll more frequently if the DirServer is unreachable (speeds up boot time).
// - Move this code into store/ or store/common, since it works with any Store.

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
	// StoreWritersFilename is the name of the Group file relative to this
	// Store's Group directory that dictates which users can write or mutate
	// the store.
	StoreWritersFilename = "StoreWriters"

	// pollInterval is how often to poll for updates to the permission file.
	pollInterval = 15 * time.Minute
)

// perm encapsulates Store mutation permission checking.
type perm struct {
	serverCtx upspin.Context

	// firstRun ensures no mutations can go through until we have resolved
	// mutation permission checking for the first time.
	firstRun sync.WaitGroup

	mu sync.Mutex // protects fields below.
	// writers stores all users allowed to mutate the store.
	writers map[upspin.UserName]bool
}

// newPerm creates a new perm for the Store in the context.
func newPerm(ctx upspin.Context) *perm {
	p := &perm{
		serverCtx: ctx,
	}
	p.firstRun.Add(1)
	return p
}

func (p *perm) updateLoop() {
	p.updateAllowedWriters()
	p.firstRun.Done()

	for range time.Tick(pollInterval) {
		p.updateAllowedWriters()
	}
}

// updateAllowedWriters retrieves and parses the Group file that rules over
// this Store's allowed writers.
func (p *perm) updateAllowedWriters() {
	const op = "store.gcp/updateAllowedWriters"
	entry, err := p.lookupMainGroupFile()
	if err != nil {
		log.Error.Printf("%s: error reading from DirServer: %s", op, err)
		return
	}
	users, err := p.allowedWriters(entry)
	if err != nil {
		log.Error.Printf("%s: error updating allowed users: %s", op, err)
		return
	}
	// Atomically update the contents of p.allowed.
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = make(map[upspin.UserName]bool)
	for _, u := range users {
		p.writers[u] = true
	}
}

// lookupMainGroupFile looks up the Group file that rules over this Store.
func (p *perm) lookupMainGroupFile() (*upspin.DirEntry, error) {
	parsed, err := path.Parse(upspin.PathName(string(p.serverCtx.UserName()) + "/Group/" + StoreWritersFilename))
	if err != nil {
		return nil, err
	}
	return p.lookup(parsed)
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (p *perm) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can unroll all authorized users.
	fakeAccess := "w,d:" + entry.Name
	acc, err := access.Parse(upspin.PathName(p.serverCtx.UserName())+"/", []byte(fakeAccess))
	if err != nil {
		return nil, err
	}

	return acc.Users(access.Write, p.load)
}

// load loads the contents of a name from the Store.
// Intended for use with access.Users.
func (p *perm) load(name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	entry, err := p.lookup(parsed)
	if err != nil {
		return nil, err
	}
	// TODO: use an entry cache here.

	return clientutil.ReadAll(p.serverCtx, entry)
}

// lookup performs a directory entry lookup on the canonical DirServer for
// the path.
func (p *perm) lookup(parsed path.Parsed) (*upspin.DirEntry, error) {
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

// isAllowedMutation reports whether the user has mutation privileges on the
// Store. It is intended to be called from Store.Put and Store.Delete.
func (p *perm) isAllowedMutation(u upspin.UserName) bool {
	p.firstRun.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	// Everyone is allowed if there are no control Groups yet.
	if len(p.writers) == 0 {
		return true
	}
	// If the special user "all@upspin.io" is present, allow all.
	if p.writers[access.All] {
		return true
	}
	return p.writers[u] // If u is not found, false is returned (default for bool).
}
