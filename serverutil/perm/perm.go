// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package perm implements mutation permission checking for servers.
package perm

import (
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	// WritersGroupFile is the name of the Group file that stores writers
	// for a Perm instance.
	WritersGroupFile = "Writers"

	// retryTimeout is the interval between re-attempts between failures.
	retryTimeout = 10 * time.Second
)

// Perm keeps track of users in the writer set for a server, as described by the
// Writers Group file. These might be users who can write blocks to a
// StoreServer or create a root on a DirServer.
// There are two ways to use Perm:
// 1) Start UpdateLoop in a goroutine with a set refresh interval; or
// 2) Call Update when the Group file
type Perm struct {
	ctx upspin.Context

	lookup LookupFunc
	target upspin.PathName

	mu      sync.Mutex // protects the map below.
	writers map[upspin.UserName]bool
}

// LookupFunc looks up the entry associated with the pathname.
type LookupFunc func(upspin.PathName) (*upspin.DirEntry, error)

// New creates a new Perm monitoring the target user's Writers Group file, using
// the provided Lookup function for lookups. The target user is typically the
// user name of a server, such as a StoreServer or a DirServer.
func New(ctx upspin.Context, target upspin.UserName, lookup LookupFunc) *Perm {
	p := &Perm{
		ctx:    ctx,
		target: upspin.PathName(target) + "/Group/" + WritersGroupFile,
		lookup: lookup,
	}
	return p
}

// UpdateLoop continuously looks for updates on WritersGroupFile.
// It must be run in a goroutine.
func (p *Perm) UpdateLoop(pollInterval time.Duration) {
	var lastErr error
	for {
		err := p.Update()
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				log.Error.Printf("serverutil/perm: error updating StoreServer's writers: %s", err)
			}
			lastErr = err
			time.Sleep(retryTimeout)
			continue
		}
		lastErr = err
		time.Sleep(pollInterval)
	}
}

// Update retrieves and parses the Group file that rules over the set of allowed
// writers.
func (p *Perm) Update() error {
	entry, err := p.lookup(p.target)
	if err != nil {
		// If the group file does not exist, reset writers map.
		if errors.Match(errors.E(errors.NotExist), err) {
			p.deleteUsers()
			return nil
		}
		return err
	}
	users, err := p.allowedWriters(entry)
	if err != nil {
		return err
	}
	p.set(users)
	return nil
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (p *Perm) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can easily use it to retrieve a
	// slice of  all authorized users.
	fakeAccess := "w,d:" + entry.Name
	acc, err := access.Parse(upspin.PathName(p.ctx.UserName())+"/", []byte(fakeAccess))
	if err != nil {
		return nil, err
	}

	return acc.Users(access.Write, p.load)
}

// load loads the contents of a name.
func (p *Perm) load(name upspin.PathName) ([]byte, error) {
	entry, err := p.lookup(name)
	if err != nil {
		return nil, err
	}
	return clientutil.ReadAll(p.ctx, entry)
}

// IsWriter reports whether the user has write privileges on this Perm.
func (p *Perm) IsWriter(u upspin.UserName) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Everyone is allowed if there is no control Group yet.
	if p.writers == nil {
		return true
	}
	// If the special user "all@upspin.io" is present, allow all.
	if p.writers[access.AllUsers] {
		return true
	}
	// Is this exact user allowed?
	if p.writers[u] {
		return true
	}
	// Maybe the domain is wildcarded. Check this case last as it's the most
	// expensive.
	_, _, domain, err := user.Parse(u)
	if err != nil {
		// Should never happen at this point.
		log.Error.Printf("serverutil/perm: unexpected error: %s", err)
		return false
	}
	return p.writers[upspin.UserName("*@"+domain)]
}

func (p *Perm) set(users []upspin.UserName) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = make(map[upspin.UserName]bool)
	for _, u := range users {
		p.writers[u] = true
	}
}

func (p *Perm) deleteUsers() {
	p.mu.Lock()
	p.writers = nil
	p.mu.Unlock()
}
