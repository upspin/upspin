// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package perm implements mutation permission checking for servers.
package perm

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
	WritersGroupFile = "Writers"

	// pollInterval is how often to poll for updates to the permission file.
	pollInterval = 2 * time.Minute

	// retryTimeout is the interval between re-attempts between failures.
	retryTimeout = 10 * time.Second
)

// Perm keeps track of users in the writer set for a server. These might be
// users who can write blocks to a StoreServer or create a root on the
// DirServer. Callers use
// There are two ways of using Perm:
// 1) Run UpdateLoop in a goroutine with a poll interval and it will refresh
//    itself by polling for the WriterGroupFile at that interval.
// 2) Call Update when the WriterGroupFile is known to have been updated.
// Method 1 is suitable for a StoreServer while method 2 is more appropriate for
// a DirServer.
type Perm struct {
	// firstRun ensures no mutations can go through until we have resolved
	// mutation permission checking for the first time.
	firstRun sync.WaitGroup

	serverCtx upspin.Context

	mu      sync.Mutex // protects the map.
	writers map[upspin.UserName]bool
}

func New(ctx upspin.Context) *Perm {
	p := &Perm{
		serverCtx: ctx,
	}
	p.firstRun.Add(1)
	go p.updateLoop()
	return p
}

// updateLoop continuously looks for updates on this StoreServer's permissions.
// It must be run in a goroutine before calling IsMutationAllowed.
func (p *Perm) updateLoop() {
	lastErr := p.update()
	if lastErr != nil {
		log.Error.Printf("store/perm: error updating StoreServer's writers: %s", lastErr)
	}
	p.firstRun.Done()

	for {
		err := p.update()
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				log.Error.Printf("store/perm: error updating StoreServer's writers: %s", err)
			}
			time.Sleep(retryTimeout)
			continue
		}
		lastErr = err
		time.Sleep(pollInterval)
	}
}

// update retrieves and parses the Group file that rules over this
// StoreServer's allowed writers.
func (p *Perm) update() error {
	entry, err := p.lookupGroupFile()
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

// lookupGroupFile looks up the Group file that rules over this StoreServer.
func (p *Perm) lookupGroupFile() (*upspin.DirEntry, error) {
	return p.lookup(upspin.PathName(string(p.serverCtx.UserName()) + "/Group/" + WritersGroupFile))
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (p *Perm) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
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
func (p *Perm) load(name upspin.PathName) ([]byte, error) {
	entry, err := p.lookup(name)
	if err != nil {
		return nil, err
	}
	// TODO: use an entry cache here.

	return clientutil.ReadAll(p.serverCtx, entry)
}

// lookup performs a directory entry lookup on the canonical DirServer for
// the path.
func (p *Perm) lookup(name upspin.PathName) (*upspin.DirEntry, error) {
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

// IsWriter reports whether the user has mutation privileges on the
// StoreServer.
func (p *Perm) IsWriter(u upspin.UserName) bool {
	p.firstRun.Wait()
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
		log.Error.Printf("store/perm: unexpected error: %s", err)
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
