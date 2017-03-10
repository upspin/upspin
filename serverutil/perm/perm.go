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
	"upspin.io/path"
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
	retryTimeout = 30 * time.Second
)

// onUpdate is a testing stub that is called after each user list update occurs.
var onUpdate = func() {}

// Perm tracks the set of users with write access to a server, as specified by
// the Writers Group file. These might be users who can write blocks to a
// StoreServer or create a root on a DirServer.
type Perm struct {
	cfg upspin.Config

	targetUser upspin.UserName
	targetFile upspin.PathName

	lookup LookupFunc
	watch  WatchFunc

	// writers is the set of users allowed to write. If it's nil, all users
	// are allowed. An empty map means no one is allowed.
	writers map[upspin.UserName]bool
	mu      sync.RWMutex // guards writers
}

// LookupFunc looks up name, as defined by upspin.DirServer.
type LookupFunc func(upspin.PathName) (*upspin.DirEntry, error)

// WatchFunc watches name, as defined by upspin.DirServer.
type WatchFunc func(upspin.PathName, int64, <-chan struct{}) (<-chan upspin.Event, error)

// New creates a new Perm monitoring the target user's Writers Group file, using
// the provided Lookup function for lookups and the Watch function to watch
// changes on the writers file. The target user is typically the user name of a
// server, such as a StoreServer or a DirServer.
func New(cfg upspin.Config, ready <-chan struct{}, target upspin.UserName, lookup LookupFunc, watch WatchFunc) (*Perm, error) {
	const op = "serverutil/perm.New"
	p := &Perm{
		cfg:        cfg,
		targetUser: target,
		targetFile: upspin.PathName(target) + "/Group/" + WritersGroupFile,
		lookup:     lookup,
		watch:      watch,
		writers:    nil, // Start open.
	}

	go func() {
		<-ready
		err := p.Update()
		if err != nil {
			log.Error.Printf("%s: %v", op, err)
			onUpdate() // Even if we failed, unblock tests.
		}
		go p.updateLoop()
	}()
	return p, nil
}

// updateLoop continuously watches for updates on WritersGroupFile.
// It must be run in a goroutine.
func (p *Perm) updateLoop() {
	const op = "serverutil/perm.updateLoop"

	var events <-chan upspin.Event
	var done chan struct{}
	var accessOrder int64
	for {
		var err error
		if events == nil {
			// Channel is not yet open. Open now.
			done = make(chan struct{})
			events, err = p.watch(upspin.PathName(p.targetUser)+"/", -1, done)
			if err != nil {
				log.Error.Printf("%s: watch: %s", op, err)
				time.Sleep(retryTimeout)
				continue
			}
		}
		e, ok := <-events
		if !ok {
			events = nil
			log.Printf("%s: watch channel closed. Re-opening...", op)
			time.Sleep(retryTimeout)
			continue
		}
		if e.Error != nil {
			log.Error.Printf("%s: watch event error: %s", op, e.Error)
			events = nil
			close(done)
			continue
		}
		// An Access file could have granted or revoked our permission
		// to watch the Writers file. Therefore, we must start the Watch
		// again, after the Access event.
		if isRelevantAccess(e.Entry.Name) && e.Order > accessOrder {
			accessOrder = e.Order
			close(done)
			events = nil
			continue
		}
		// Process event.
		if e.Entry.Name != p.targetFile {
			continue
		}
		if e.Delete {
			p.deleteUsers()
			continue
		}
		err = p.updateUsers(e.Entry)
		if err != nil {
			log.Error.Printf("%s: updateUsers: %s", op, err)
		}
	}
}

// isRelevantAccess access reports whether name is an Access file in a Group
// directory or at the root.
func isRelevantAccess(name upspin.PathName) bool {
	parsed, err := path.Parse(name)
	if err != nil {
		log.Error.Printf("serverutil/perm.isRelevantAccess: unexpected error: %s", err)
		return false
	}
	file := parsed.FilePath()
	return file == "Access" || file == "Group/Access"
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
	log.Printf("serverutil/perm: Setting writers to: %v", users)
	p.set(users)
	onUpdate()
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
	return clientutil.ReadAll(p.cfg, entry)
}

// IsWriter reports whether the user has write privileges on this Perm.
func (p *Perm) IsWriter(u upspin.UserName) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// Everyone is allowed if there is no Writers Group file.
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
	onUpdate()
}
