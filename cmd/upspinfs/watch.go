// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(p): Similarities between this and dir/dircache are not accidental,
// this was derived from dir/dircache/peroxied.go. I may eventually
// merge them after they stop changing and I have a better idea of
// exactly what needs to be abstracted.

// +build !windows

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
	refreshInterval      = 30 * time.Second
)

// watchedRoot contains information about watched user directories.
type watchedRoot struct {
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

	die   chan bool // Closed to tell watcher to die.
	dying chan bool // Closed to confirm watcher is dying.

	// retryInterval is the interval between Watch attempts.
	retryInterval time.Duration

	watchSupported bool
}

// watchedRoots maps a user name and the relevant cached directory.
type watchedRoots struct {
	sync.Mutex

	closing        bool      // When this is true do not allocate any new watchers.
	f              *upspinFS // File system we are watching for.
	m              map[upspin.UserName]*watchedRoot
	invalidateChan chan *node
}

func newWatchedDirs(f *upspinFS) *watchedRoots {
	w := &watchedRoots{
		f:              f,
		m:              make(map[upspin.UserName]*watchedRoot),
		invalidateChan: make(chan *node, 100),
	}
	go w.invalidater()
	return w
}

// add increments the reference count for the relevant directory and
// creates a watcher if none is running for it.
func (w *watchedRoots) add(name upspin.PathName) {
	p, err := path.Parse(name)
	if err != nil {
		log.Debug.Printf("upspinfs.watch: %s", err)
		return
	}
	w.Lock()
	defer w.Unlock()
	user := p.User()

	if d := w.m[user]; d != nil {
		d.ref++
		return
	}
	d := &watchedRoot{
		ref:   1,
		f:     w.f,
		user:  user,
		die:   make(chan bool),
		dying: make(chan bool),
	}
	w.m[user] = d
	go d.watcher()
}

// remove decrements the reference count for the relevant directory and
// kills any watcher if the reference count goes to zero.
func (w *watchedRoots) remove(name upspin.PathName) {
	p, err := path.Parse(name)
	if err != nil {
		log.Debug.Printf("upspinfs.watch: %s", err)
		return
	}
	w.Lock()
	defer w.Unlock()
	user := p.User()

	if d := w.m[user]; d != nil {
		d.ref--
		if d.ref == 0 {
			delete(w.m, user)
			close(d.die)
		}
		if d.ref < 0 {
			log.Error.Printf("watchedRoots.remove ref %d", d.ref)
		}
	}
}

// watchSupported reports whether name is on a server that supports watch. If name
// is invalid, we know nothing about it, or watch isn't supported return false.
func (w *watchedRoots) watchSupported(name upspin.PathName) bool {
	p, err := path.Parse(name)
	if err != nil {
		return false
	}
	w.Lock()
	defer w.Unlock()
	d, ok := w.m[p.User()]
	return ok && d.watchSupported
}

// refresh refreshes the node if the relevant directory does not support Watch.
// Assumes n is locked.
func (w *watchedRoots) refresh(n *node) error {
	const op errors.Op = "refresh"

	// Watch is handling refreshes.
	if n.doNotRefresh {
		return nil
	}

	// Don't refresh special nodes.
	if n.t != otherNode {
		return nil
	}

	if n.refreshTime.After(time.Now()) {
		return nil
	}

	// Don't refresh nodes for files we currently have open since
	// we are the correct source.
	if len(n.handles) > 0 {
		return nil
	}

	p, err := path.Parse(n.uname)
	if err != nil {
		return e2e(errors.E(op, err))
	}
	w.Lock()
	user := p.User()

	d, ok := w.m[user]
	if ok && d.watchSupported {
		// Don't refresh if the DirServer supports Watch.
		n.doNotRefresh = true
		w.Unlock()
		return nil
	}
	w.Unlock()

	// Ask the Dirserver.
	_, de, err := n.lookup(n.uname)
	if err != nil {
		n.refreshTime = time.Now().Add(refreshInterval / 4)
		n.f.removeMapping(n.uname)
		return e2e(errors.E(op, err))
	}

	// Nothing changed.
	if n.seq == de.Sequence {
		n.refreshTime = time.Now().Add(refreshInterval)
		return nil
	}
	n.seq = de.Sequence

	// Update cached info for node.
	mode := os.FileMode(unixPermissions)
	if de.IsDir() {
		mode |= os.ModeDir
	}
	if de.IsLink() {
		mode |= os.ModeSymlink
	}
	size, err := lstatSize(de, n)
	if err != nil {
		n.f.removeMapping(n.uname)
		return e2e(errors.E(op, err))
	}
	n.attr.Size = size
	n.attr.Mode = mode
	if de.IsLink() {
		n.link = upspin.PathName(de.Link)
	}

	select {
	default:
		n.refreshTime = time.Now().Add(refreshInterval / 4)
	case w.invalidateChan <- n:
		n.refreshTime = time.Now().Add(refreshInterval)
	}
	return nil
}

// invalidate tells the kernel to purge data about a node. It must be called
// with no locks held since it could generate a FUSE request causing a
// deadlock in the kernel.
func (f *upspinFS) invalidate(n *node) {
	f.server.InvalidateNodeAttr(n)
	f.server.InvalidateNodeData(n)
}

// invalidater is a goroutine that loops calling invalidate. It exists so
// that invalidations can be done outside of FUSE RPCs.  Otherwise there
// are deadlocking possibilities.
func (w *watchedRoots) invalidater() {
	for {
		n := <-w.invalidateChan
		n.f.invalidate(n)
	}
}

// watcher watches a directory and caches any changes to something already in the LRU.
func (d *watchedRoot) watcher() {
	log.Debug.Printf("upspinfs.watcher %s", d.user)
	defer close(d.dying)

	// We have no past so just watch what happens from now on.
	d.sequence = upspin.WatchNew

	d.retryInterval = initialRetryInterval
	d.watchSupported = true
	for {
		err := d.watch()
		if err == nil {
			log.Debug.Printf("upspinfs.watcher %s exiting", d.user)
			// The watch routine only returns if the watcher has been told to die
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
func (d *watchedRoot) watch() error {
	dir, err := d.f.dirLookup(d.user)
	if err != nil {
		return err
	}
	name := upspin.PathName(string(d.user) + "/")
	done := make(chan struct{})
	event, err := dir.Watch(name, d.sequence, done)
	if err != nil {
		close(done)
		return err
	}

	// If Watch succeeds, go back to the initial interval.
	d.retryInterval = initialRetryInterval

	// Loop receiving events until we are told to stop or the event stream is closed.
	for {
		select {
		case <-d.die:
			// Drain events after the close or future RPCs on the same
			// connection could hang.
			close(done)
			for range event {
			}
			return nil
		case e, ok := <-event:
			if !ok {
				close(done)
				return errors.Str("Watch event stream closed")
			}
			if e.Error != nil {
				log.Debug.Printf("upspinfs: Watch(%q) error: %s", name, e.Error)
			} else {
				log.Debug.Printf("upspinfs: Watch(%q) entry: %s (delete=%t)", name, e.Entry.Name, e.Delete)
			}
			if err := d.handleEvent(&e); err != nil {
				close(done)
				return err
			}
		}
	}
}

func (d *watchedRoot) handleEvent(e *upspin.Event) error {
	// Something odd happened?
	if e.Error != nil {
		return e.Error
	}
	f := d.f

	// We will have to invalidate the directory also.
	dir := path.DropPath(e.Entry.Name, 1)

	// Is this a file we are watching?
	f.Lock()
	n, ok := f.nodeMap[e.Entry.Name]
	if !e.Delete {
		// We can't check for insequence since we don't have a
		// sequence for when we put it in the enoentMap so
		// just take it out. Worst case is we just forgot
		// an optimization.
		delete(f.enoentMap, e.Entry.Name)
	}
	dirn := f.nodeMap[dir]
	// If a file has just been put to or deleted from a directory,
	// the directory certainly exists. Make sure we don't think that
	// it isn't there.
	delete(f.enoentMap, dir)
	f.Unlock()
	if !ok {
		return nil
	}

	// Ignore events that precede what we have done to a file.
	n.Lock()
	if n.uname != e.Entry.Name || e.Entry.Sequence <= n.seq {
		// The node changed before we locked it.
		n.Unlock()
		return nil
	}

	// Don't update files being written.
	if n.cf != nil && n.cf.dirty {
		n.Unlock()
		return nil
	}

	// At this point we know that n is an old version of the node
	// for e.Entry.Name and that it hasn't been changed locally.
	if e.Delete {
		// If the uname changed between the time we looked it up a few lines
		// above and here, it means a Rename intervened, and reused the node.
		// In that case we must not mark the node as deleted. Since Rename already
		// made sure that old file is no longer used, it should be OK to skip
		// this part.
		if n.uname == e.Entry.Name {
			f.doesNotExist(n.uname)
			n.deleted = true
		}
	} else if n.cf != nil {
		// If we've changed an open file, forget the
		// mapping of name to node so that new opens
		// will get the new file.
		f.removeMapping(n.uname)
		n.deleted = false
	} else {
		// Update cached info for node.
		mode := os.FileMode(unixPermissions)
		if e.Entry.IsDir() {
			mode |= os.ModeDir
		}
		if e.Entry.IsLink() {
			mode |= os.ModeSymlink
		}
		size, err := lstatSize(e.Entry, n)
		if err == nil {
			n.attr.Size = size
		} else {
			log.Debug.Printf("upspinfs.watch: %s", err)
		}
		n.attr.Mode = mode
		if e.Entry.IsLink() {
			n.link = upspin.PathName(e.Entry.Link)
		}
		n.attr.Mtime = e.Entry.Time.Go()
		n.deleted = false
	}
	n.Unlock()

	// Invalidate what the kernel knows about the file and its
	// directory.
	//
	// invalidate has to be outside of locks because it can trigger
	// another FUSE request deadlocking the kernel.
	f.invalidate(n)
	if dirn != nil && dir != e.Entry.Name {
		f.invalidate(dirn)
	}
	return nil
}
