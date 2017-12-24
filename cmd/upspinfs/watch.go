// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(p): Similarities between this and dir/dircache are not accidental,
// this was derived from dir/dircache/peroxied.go. I may eventually
// merge them after they stop changing and I have a better idea of
// exactly what need to be abstracted.

// +build !windows
// +build !openbsd

package main // import "upspin.io/cmd/upspinfs"

import (
	"os"
	"sync"
	"time"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	initialRetryInterval = time.Second
	maxRetryInterval     = time.Minute
)

// watchedDir contains information about watched user directories.
type watchedDir struct {
	f     *upspinFS
	atime time.Time // time of last access
	user  upspin.UserName

	// ref is a count of user files we are watching in user's directory.
	ref int

	// sequence is the last sequence number seen in a watch. It is only
	// set outside the watcher before any watcher starts
	// while reading the log files.
	sequence int64

	// ep is only used outside the watcher and is the
	// endpoint of the server being watched.
	ep upspin.Endpoint

	die   chan bool // channel used to tell watcher to die
	dying chan bool // channel used to confirm watcher is dying

	// For retrying a watch.
	retryInterval time.Duration

	watchSupported bool
}

// watchedDirs is used to translate between a user name and the relevant cached directory.
type watchedDirs struct {
	sync.Mutex

	closing bool      // when this is true do not allocate any new watchers
	f       *upspinFS // File system we are watching for.
	m       map[upspin.UserName]*watchedDir
}

func newWatchedDirs(f *upspinFS) *watchedDirs {
	return &watchedDirs{f: f, m: make(map[upspin.UserName]*watchedDir)}
}

func (w *watchedDirs) add(name upspin.PathName) {
	p, err := path.Parse(name)
	if err != nil {
		log.Debug.Printf("upspinfs.watch: %s", err)
		return
	}
	w.Lock()
	defer w.Unlock()
	user := p.User()

	d, ok := w.m[user]
	if ok {
		d.ref++
		return
	}
	d = &watchedDir{f: w.f, user: user, die: make(chan bool), dying: make(chan bool)}
	w.m[user] = d
	go d.watcher()
}

func (w *watchedDirs) remove(name upspin.PathName) {
	p, err := path.Parse(name)
	if err != nil {
		log.Debug.Printf("upspinfs.watch: %s", err)
		return
	}
	w.Lock()
	defer w.Unlock()
	user := p.User()

	d, ok := w.m[user]
	if ok {
		d.ref--
		if d.ref == 0 {
			close(d.die)
		}
		return
	}
}

// watcher watches a directory and caches any changes to something already in the LRU.
func (d *watchedDir) watcher() {
	log.Debug.Printf("upspinfs.Watcher %s", d.user)
	defer close(d.dying)

	// We have no past so just watch what happens from now on.
	d.sequence = upspin.WatchNew

	d.retryInterval = initialRetryInterval
	for {
		err := d.watch()
		if err == nil {
			log.Debug.Printf("upspinfs.Watcher %s exiting", d.user)
			// watch() only returns if the watcher has been told to die
			// or if there is an error requiring a new Watch.
			return
		}
		if err == upspin.ErrNotSupported {
			// Can't survive this.
			d.watchSupported = false
			log.Debug.Printf("upspinfs.watcher: %s: %s", d.user, err)
			return
		}
		if errors.Is(errors.Invalid, err) {
			// A bad record in the log or a bad sequence number. Reread current state.
			log.Info.Printf("upspinfs.watcher restarting Watch: %s: %s", d.user, err)
			d.sequence = upspin.WatchNew
		} else {
			log.Info.Printf("upspinfs.watcher: %s: %s", d.user, err)
		}

		select {
		case <-time.After(d.retryInterval):
			d.retryInterval *= 2
			if d.retryInterval > maxRetryInterval {
				d.retryInterval = maxRetryInterval
			}
		}
	}
}

// watch loops receiving watch events. It returns nil if told to die.
// Otherwise it returns whatever error was encountered.
func (d *watchedDir) watch() error {
	dir, err := d.f.dirLookup(d.user)
	if err != nil {
		return err
	}
	name := upspin.PathName(string(d.user) + "/")
	done := make(chan struct{})
	defer close(done)
	event, err := dir.Watch(name, d.sequence, done)
	if err != nil {
		return err
	}

	// If Watch succeeds, go back to the initial interval.
	d.retryInterval = initialRetryInterval

	// Loop receiving events until we are told to stop or the event stream is closed.
	for {
		select {
		case <-d.die:
			return nil
		case e, ok := <-event:
			if !ok {
				return errors.Str("Watch event stream closed")
			}
			if e.Error != nil {
				log.Debug.Printf("upspinfs: Watch(%q) error: %s", name, e.Error)
			} else {
				log.Debug.Printf("upspinfs: Watch(%q) entry: %s (delete=%t)", name, e.Entry.Name, e.Delete)
			}
			if err := d.handleEvent(&e); err != nil {
				return err
			}
		}
	}
}

func (d *watchedDir) handleEvent(e *upspin.Event) error {
	// Something odd happened?
	if e.Error != nil {
		return e.Error
	}
	f := d.f

	// Is this a file we are watching?
	f.Lock()
	n, ok := f.nodeMap[e.Entry.Name]
	if f.enoentMap[e.Entry.Name] && !e.Delete {
		// We can't check for insequence since we don't have a
		// sequence for when we put it in the enoentMap so
		// just take it out. Worst case is we just forgot
		// an optimization.
		delete(f.enoentMap, e.Entry.Name)
	}
	f.Unlock()
	if !ok {
		return nil
	}

	// Ignore events that precede what we have done to a file.
	n.Lock()
	if e.Entry.Sequence <= n.seq {
		n.Unlock()
		return nil
	}

	// Don't update files being written.
	if n.cf != nil && n.cf.dirty {
		n.Unlock()
		return nil
	}

	if e.Delete {
		f.doesNotExist(n.uname)
		n.deleted = true
	} else if n.cf != nil {
		// If we've changed an open file, forget the
		// mapping of name to node so that new opens
		// will get the new file.
		f.removeMapping(n.uname)
	} else {
		// Update cached info for node.
		mode := os.FileMode(unixPermissions)
		if e.Entry.IsDir() {
			mode |= os.ModeDir
		}
		if e.Entry.IsLink() {
			mode |= os.ModeSymlink
		}
		size, err := e.Entry.Size()
		if err == nil {
			n.attr.Size = uint64(size)
		} else {
			log.Debug.Printf("upspinfs.watch: %s", err)
		}
		n.attr.Mode = mode
		if e.Entry.IsLink() {
			n.link = upspin.PathName(e.Entry.Link)
		}
		n.attr.Mtime = e.Entry.Time.Go()
	}
	n.Unlock()

	// invalidate has to be outside of locks because it can trigger
	// another FUSE requests deadlocking the system.
	f.invalidate(n)
	return nil
}
