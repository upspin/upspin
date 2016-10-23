// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

// This file deals with mutation (put, delete) permission on Store.
//
// TODOs:
// - First time around looking for Group files, lock everything until we're
//   done contacting the DirServer. This prevents unauthorized access in case
//   it is already setup.
// - Poll more frequently if there is no access control setup, so initial
//   setup of one is faster.
// - Move this code into store/ or store/common.

import (
	"crypto/sha256"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// perm encapsulates permission checking and updating the access list.
type perm struct {
	serverCtx upspin.Context

	// allowedHash is the hash of all block References that compose the
	// list of users allowed to mutate the store. It servers the purpose of
	// avoiding opening and parsing the contents of every Reference, since
	// they're expected to change slowly.
	allowedHash string

	mu sync.Mutex // protects the map below.
	// allowed stores all users allowed to mutate the store.
	allowed map[upspin.UserName]bool
}

const pollInterval = 15 * time.Minute

func newPerm(ctx upspin.Context) *perm {
	return &perm{
		serverCtx: ctx,
	}
}

func (p *perm) startUpdateLoop() {
	go p.updateLoop()
}

func (p *perm) updateLoop() {
	p.updateAllowedUsers()

	t := time.Tick(pollInterval)
	for {
		select {
		case <-t:
			p.updateAllowedUsers()
		}
	}
}

func (p *perm) updateAllowedUsers() {
	const op = "store.gcp/updateAllowedUsers"
	entries, err := p.lookupAllGroupFiles()
	if err != nil {
		log.Error.Printf("%s: error reading from DirServer: %s", op, err)
	}
	// Have we processed these entries?
	blockHash := sha256.New()
	for _, entry := range entries {
		for _, blk := range entry.Blocks {
			_, err := blockHash.Write([]byte(blk.Location.Reference))
			if err != nil {
				log.Error.Printf("%s: error writing hash: %s", op, err)
			}
		}
	}
	newHash := blockHash.Sum(nil)
	if newHash == p.allowedHash {
		// No change. No need to read the blocks.
		return
	}
	// Contents have changed, update set of allowed users.
	users, err := p.readAllowedUsers(entries)
	if err != nil {
		log.Error.Printf("%s: error updating allowed users: %s", op, err)
		return
	}
	// Atomically update the contents of p.allowed and the hash.
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowedHash = newHash
	p.allowed = make(map[upspin.UserName]bool)
	for _, u := range users {
		p.allowed[u] = true
	}
}

func (p *perm) lookupAllGroupFiles() ([]upspin.DirEntry, error) {
	dir, err := bind.DirServer(p.serverCtx, p.serverCtx.DirEndpoint())
	if err != nil {
		return nil, err
	}
	// Glob all group files for this server, since this Store could be
	// backing multiple domains.
	return dir.Glob(string(p.serverCtx.UserName()) + "/Group/*")
}

// readAllowedUsers reads the contents of all entries, expands them as Group
// files and returns the slice of allowed users.
func (p *perm) readAllowedUsers(entries []upspin.DirEntry) ([]upspin.UserName, error) {
	var users []upspin.UserName
	for _, entry := range entries {
		data, err := clientutil.ReadAll(p.serverCtx, entry)
		if err != nil {
			return nil, err
		}
		p, err := path.Parse(entry.Name)
		if err != nil {
			return nil, err
		}
		paths, err := access.ParseGroup(p, data)
		for _, u := range paths {
			if u.IsRoot() {
				users = append(users, u.User())
			}
			// TODO: we don't allow remote Groups. Should we?
		}
	}
	return users, nil
}

// isAllowedMutation reports whether the user has mutation privileges on the
// Store. It is intended to be called from Store.Put and Store.Delete.
func (p *perm) isAllowedMutation(u upspin.UserName) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Everyone is allowed if there are no control Groups yet.
	if p.allowedHash == "" {
		return true
	}
	// If the special user "all@upspin.io" is present, allow to all.
	if p.allowed[access.All] {
		return true
	}
	return p.allowed[u] // If u is not found, false is returned (default value of bool).
}
