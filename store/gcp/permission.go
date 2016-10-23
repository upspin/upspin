// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file deals with mutation (put, delete) permissions on Store.
//
// TODOs:
// - Cache references so we don't need to retrieve the contents every time.
// - Poll more frequently if there is no access control Group setup, so initial
//   setup of one is faster.
// - Move this code into store/ or store/common.

import (
	"sync"
	"time"

	"strings"
	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const pollInterval = 15 * time.Minute

// perm encapsulates permission checking and updating the access list.
type perm struct {
	serverCtx upspin.Context

	// firstRun ensures no writes can go through until we have resolved
	// mutation permission checking the first time.
	firstRun sync.WaitGroup

	mu sync.Mutex // protects fields below.
	// writers stores all users allowed to mutate the store.
	writers map[upspin.UserName]bool
}

func newPerm(ctx upspin.Context) *perm {
	p := &perm{
		serverCtx: ctx,
	}
	p.firstRun.Add(1)
	return p
}

func (p *perm) startUpdateLoop() {
	go p.updateLoop()
}

func (p *perm) updateLoop() {
	p.updateAllowedWriters()
	p.firstRun.Done()

	t := time.Tick(pollInterval)
	for {
		select {
		case <-t:
			p.updateAllowedWriters()
		}
	}
}

// updateAllowedWriters retrieves and parses the Group file that rules over
// this Store's allowed writers.
func (p *perm) updateAllowedWriters() {
	const op = "store.gcp/updateAllowedWriters"
	entry, err := p.lookupGroupFile()
	if err != nil {
		log.Error.Printf("%s: error reading from DirServer: %s", op, err)
		return
	}
	users, err := p.readAllowedWriters(entry)
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

func (p *perm) lookupGroupFile() (*upspin.DirEntry, error) {
	netAddr, err := cleanNetAddr(p.serverCtx.StoreEndpoint())
	if err != nil {
		return nil, err
	}
	// TODO: use access.GroupDir.
	parsed, err := path.Parse(upspin.PathName(string(p.serverCtx.UserName()) + "/Group/" + netAddr))
	if err != nil {
		return nil, err
	}
	return p.dirLookup(parsed)
}

// readAllowedWriters reads the contents of the entry, expands it as Group,
// expanding recursively if needed, and returns the slice of users allowed to
// write to the store.
func (p *perm) readAllowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can unroll all users.
	// TODO: perhaps we need to refactor and export a recursive version of
	// access.ParseGroup. This is an ok hack for now.
	fakeAccess := "w,d:" + entry.Name
	acc, err := access.Parse(upspin.PathName(p.serverCtx.UserName())+"/", []byte(fakeAccess))
	if err != nil {
		return nil, err
	}

	return acc.Users(access.Write, p.loadPath)
}

// loadPath loads the contents of a name from the Store.
// Intended for use with access.Users.
func (p *perm) loadPath(name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	entry, err := p.dirLookup(parsed)
	if err != nil {
		return nil, err
	}
	// TODO: use an entry cache here.

	return clientutil.ReadAll(p.serverCtx, entry)
}

// dirLookup performs a directory entry lookup on the canonical DirServer for
// the path.
func (p *perm) dirLookup(parsed path.Parsed) (*upspin.DirEntry, error) {
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

// cleanNetAddr returns the NetAddr of an endpoint with any prefix and the port
// number stripped.
func cleanNetAddr(e upspin.Endpoint) (string, error) {
	addr := string(e.NetAddr)
	if len(addr) < 4 { // at least 'a.co'
		return "", errors.E(errors.Invalid, errors.Str("NetAddr too short"))
	}
	prefix := strings.LastIndex(addr, "/")
	if prefix >= 0 && len(addr) > prefix {
		addr = addr[prefix+1:]
	}
	port := strings.Index(addr, ":")
	if port >= 0 {
		addr = addr[:port]
	}
	return addr, nil
}
