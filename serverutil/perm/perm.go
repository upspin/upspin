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
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	// WritersGroupFile is the name of the Group file that specifies writers
	// for a Perm instance.
	WritersGroupFile = "Writers"
)

const (
	// retryTimeout is the interval between re-attempts between failures.
	retryTimeout = 10 * time.Second
)

// Perm tracks the set of users with write access to a server, as specified by
// the Writers Group file. These might be users who can write blocks to a
// StoreServer or create a root on a DirServer.
type Perm struct {
	ctx upspin.Context

	targetUser upspin.UserName
	targetFile upspin.PathName

	done   chan struct{}
	events <-chan upspin.Event

	mu           sync.Mutex // protects the fields below.
	eventCounter int64      // counts events on channel; mostly for testing.
	eventCond    *sync.Cond // informs when eventCounter is updated.
	writers      map[upspin.UserName]bool
}

// New creates a new Perm monitoring the target user's Writers Group file, using
// the provided Lookup function for lookups and the Watch function to watch
// changes on the writers file. The target user is typically the user name of a
// server, such as a StoreServer or a DirServer.
func New(ctx upspin.Context, target upspin.UserName) (*Perm, error) {
	const op = "serverutil/perm.New"
	p := &Perm{
		ctx:        ctx,
		targetUser: target,
		targetFile: upspin.PathName(target) + "/Group/" + WritersGroupFile,
	}
	p.eventCond = sync.NewCond(&p.mu)
	err := p.Update()
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
	}
	go p.updateLoop()
	return p, nil
}

// updateLoop continuously watches for updates on WritersGroupFile.
// It must be run in a goroutine.
func (p *Perm) updateLoop() {
	const op = "serverutil/perm.updateLoop"
	err := p.watchTarget()
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
	}
	for {
		e, ok := <-p.events
		if !ok {
			// Channel was closed. Re-open.
			err := p.watchTarget()
			if err != nil {
				log.Printf("%s: watch: %s", op, err)
				time.Sleep(retryTimeout)
			}
			continue
		}
		if e.Error != nil {
			log.Error.Printf("%s: event error: %s", op, e.Error)
			close(p.done)
			continue // will next be !ok and re-start watcher.
		}
		// Process event.
		if e.Entry.Name != p.targetFile {
			continue
		}
		if e.Delete {
			p.deleteUsers()
		}
		err = p.updateUsers(e.Entry)
		if err != nil {
			log.Error.Printf("%s: updateUsers: %s", op, err)
		}
		p.mu.Lock()
		p.eventCounter++
		p.eventCond.Signal()
		p.mu.Unlock()
	}
}

// Update retrieves and parses the Group file that rules over the set of allowed
// writers. This is mostly only exported for testing, but servers may use it to
// force immediate updates.
func (p *Perm) Update() error {
	entry, err := p.lookup(p.targetFile)
	if err != nil {
		// If the group file does not exist, reset writers map.
		if errors.Match(errors.E(errors.NotExist), err) {
			p.deleteUsers()
			return nil
		}
		return err
	}
	return p.updateUsers(entry)
}

// updateUsers reads the writers Group file entry and updates the user set.
func (p *Perm) updateUsers(entry *upspin.DirEntry) error {
	users, err := p.allowedWriters(entry)
	if err != nil {
		return err
	}
	p.set(users)
	return nil
}

// watchTarget creates a new watcher for the target file and saves the done
// channel and the returned events channel.
func (p *Perm) watchTarget() error {
	p.done = make(chan struct{})
	// Watch the root of the user.
	events, err := p.watch(upspin.PathName(p.targetUser)+"/", -1, p.done)
	if err != nil {
		return err
	}
	// Only overwrite p.events when successful, so updateLoop keeps
	// getting the previous (likely closed) channel.
	p.events = events
	return nil
}

// allowedWriters reads the contents of the entry, interprets it exactly as
// an access Group file, expanding recursively if needed, and returns the slice
// of users allowed to write to the store.
func (p *Perm) allowedWriters(entry *upspin.DirEntry) ([]upspin.UserName, error) {
	// Pretend this is an Access file, so we can easily use it to retrieve a
	// slice of  all authorized users.
	fakeAccess := "w,d:" + entry.Name
	access.RemoveGroup(entry.Name)
	acc, err := access.Parse(upspin.PathName(p.targetUser+"/"), []byte(fakeAccess))
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

func (p *Perm) lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	dir, err := bind.DirServerFor(p.ctx, name)
	if err != nil {
		return nil, err
	}
	return dir.Lookup(name)
}

func (p *Perm) watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	dir, err := bind.DirServerFor(p.ctx, name)
	if err != nil {
		return nil, err
	}
	return dir.Watch(name, order, done)
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
