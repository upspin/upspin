// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

// This file defines structures that keep track of individual target directories.
// It particular it keeps a count of entries from the directory still in the LRU
// and handles refreshing of directory entries.

import (
	"sync"
	"time"

	"upspin.io/bind"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// proxiedDir contains information about a proxied user directoies.
type proxiedDir struct {
	l     *clog
	ep    *upspin.Endpoint // endpoint for directory server
	order int64            // last order seen in watch
	atime time.Time        // time of last access
	user  upspin.UserName

	die   chan bool // channel used to tell watcher to die
	dying chan bool // channel used to confirm watcher is dying
}

// proxiedDirs is used to translate between a user name and the relevant cached directory.
type proxiedDirs struct {
	sync.Mutex

	closing bool // when this is true do not allocate any new watchers
	l       *clog
	m       map[upspin.UserName]*proxiedDir
}

func newProxiedDirs(l *clog) *proxiedDirs {
	return &proxiedDirs{m: make(map[upspin.UserName]*proxiedDir), l: l}
}

// close terminates all watchers.
func (p *proxiedDirs) close() {
	p.Lock()
	defer p.Unlock()
	if p.closing {
		return
	}
	p.closing = true
	for _, d := range p.m {
		d.close()
	}
}

// proxyFor saves the endpoint and makes sure it is being watched.
func (p *proxiedDirs) proxyFor(name upspin.PathName, ep *upspin.Endpoint) {
	p.Lock()
	defer p.Unlock()
	if p.closing {
		return
	}

	parsed, err := path.Parse(name)
	if err != nil {
		log.Info.Printf("parse error on a cleaned name: %s", name)
		return
	}
	u := parsed.User()
	d := p.m[u]
	if d == nil {
		d = &proxiedDir{l: p.l, ep: ep, user: u}
		p.m[u] = d
	}

	// Remember when we last accessed this proxied directory.
	// TODO: Use this time to stop listening to directories we
	// haven't looked at in a long time. We will also have to
	// forget about cached information for them if we stop
	// watching.
	d.atime = time.Now()

	// If the endpoint changed, kill off the current watcher.
	if d.ep != ep {
		d.close()
	}

	// Start a watcher if none is running.
	d.ep = ep
	if d.die == nil {
		d.die = make(chan bool)
		d.dying = make(chan bool)
		go d.watcher()
	}
}

// setOrder remembers an order read from the logfile.
func (p *proxiedDirs) setOrder(name upspin.PathName, order int64) {
	p.Lock()
	defer p.Unlock()
	if p.closing {
		return
	}

	parsed, err := path.Parse(name)
	if err != nil {
		log.Info.Printf("parse error on a cleaned name: %s", name)
		return
	}
	u := parsed.User()
	d := p.m[u]
	if d == nil {
		d = &proxiedDir{l: p.l, user: u}
		p.m[u] = d
	}
	d.order = order
}

// close terminates the goroutines associated with a proxied dir.
func (d *proxiedDir) close() {
	if d.die != nil {
		close(d.die)
		<-d.dying
		d.die = nil
	}
}

// watcher watches a directory and caches any changes to something already in the LRU.
func (d *proxiedDir) watcher() {
	var dir upspin.DirServer
	var err error
	var doneChan chan struct{}
	var eventChan <-chan upspin.Event
outer:
	for {
		dir, err = bind.DirServer(d.l.ctx, *d.ep)
		if err != nil {
			// I don't log because this would generate boundless
			// logging if we are disconnected or the server is down.

			// Try again later.
			time.Sleep(10 * time.Second)
			continue
		}
		doneChan = make(chan struct{})
		eventChan, err = dir.Watch(upspin.PathName(string(d.user)+"/"), d.order, doneChan)
		if err != nil {
			if err == upspin.ErrNotSupported {
				log.Info.Printf("grpc/dircacheserver.watcher: %s: %s", d.user, err)
				break outer
			}
			close(doneChan)
			// Try again later
			time.Sleep(10 * time.Second)
			continue
		}

		// Loop receiving events or until we are told to stop.
	inner:
		for {
			select {
			case <-d.die:
				break outer
			case e, ok := <-eventChan:
				if !ok {
					// The server has closed the channel.
					close(doneChan)

					// Try again later.
					time.Sleep(10 * time.Second)
					break inner
				}
				d.handleEvent(&e)
			}
		}
	}
	if doneChan != nil {
		close(doneChan)
	}
	close(d.dying)
}

func (d *proxiedDir) handleEvent(e *upspin.Event) {
	// Something odd happened?
	if e.Error != nil {
		log.Info.Printf("grpc/dircacheserver.handleEvent: %s", e.Error)
		return
	}

	// Is this an Event we care about?
	log.Info.Printf("watch entry %s %v", e.Entry.Name, e)
	_, ok := d.l.lru.Get(lruKey{name: e.Entry.Name, glob: false})
	if !ok {
		dirName := path.DropPath(e.Entry.Name, 1)
		if dirName == e.Entry.Name {
			return
		}
		_, ok := d.l.lru.Get(lruKey{name: dirName, glob: true})
		if !ok {
			return
		}
	}

	// This is an event we care about.
	d.order = e.Order
	op := lookupReq
	if e.Delete {
		op = deleteReq
	}
	d.l.logRequest(op, e.Entry.Name, nil, e.Entry)
}
