// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// TODOs:
// - The watcher "tails" the log, starting from a given sequence number. It is
//   done in a goroutine because the sequence can be very far from the current state
//   and we don't want to block the caller until all such state is sent on the
//   Event channel. However, once the watcher has caught up with the current
//   state of the Tree, there's no longer a need for a goroutine or for reading
//   the log directly (and thus spend time in disk I/O, unmarshalling, etc). We
//   can simply note that the end of file was reached, quit the goroutine and
//   send events as they come in. This requires some extra synchronization code
//   and ensuring that sending does not block the Tree (we can keep the
//   goroutine if we don't want to impose a short timeout on the channel).

import (
	"sync/atomic"
	"time"

	"upspin.io/dir/server/serverlog"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	// watcherTimeout is the timeout to send notifications on a watcher
	// channel. It happens in a goroutine, so it's safe to hang for a while.
	watcherTimeout = 1 * time.Minute
)

var (
	errTimeout = errors.E(errors.IO, "channel operation timed out")
	errClosed  = errors.E(errors.IO, "channel closed")
)

// watcher holds together the done channel and the event channel for a given
// watch point.
type watcher struct {
	// The path name this watcher watches.
	path path.Parsed

	// events is the Event channel with the client. It is write-only.
	events chan *upspin.Event

	// done is the client's done channel. When it's closed, this watcher
	// dies.
	done <-chan struct{}

	// hasWork is an internal channel that the Tree uses to tell the watcher
	// goroutine to look for work at the end of the log.
	hasWork chan bool

	// log is a reader instance of the Tree's log that keeps track of this
	// watcher's progress.
	log *serverlog.Reader

	// closed indicates whether the watcher is closed (1) or open (0).
	// It must be loaded and stored atomically.
	closed int32

	// shutdown is closed by Tree.Close to signal that the watcher should
	// exit. It is never closed by the watcher itself.
	shutdown chan struct{}

	// doneFunc must be called by this watcher before it exits its watch
	// loop. It decrements the owning tree's watchers wait group.
	doneFunc func()
}

// Watch implements upspin.DirServer.Watch.
func (t *Tree) Watch(p path.Parsed, sequence int64, done <-chan struct{}) (<-chan *upspin.Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// First, ensure the tree is not shutting down.
	select {
	case <-t.shutdown:
		return nil, errors.Str("can't start Watch; tree shutting down")
	default:
	}

	// Watch can watch non-existent files, but not non-existent roots.
	// Therefore, we ensure the root exists before we proceed.
	err := t.loadRoot()
	if err != nil {
		return nil, err
	}

	// Clone the logs so we can keep reading it while the current tree
	// continues to be updated (we're about to unlock this tree).
	cLog, err := t.user.NewReader()
	if err != nil {
		return nil, err
	}

	// Create a watcher, but do not attach it to any node yet.
	// TODO: limit number of watchers on any given node/tree?
	w := &watcher{
		path:     p,
		events:   make(chan *upspin.Event),
		done:     done,
		hasWork:  make(chan bool, 1),
		log:      cLog,
		closed:   0,
		shutdown: t.shutdown,
	}
	w.doneFunc = func() {
		// Remove this watcher from watchers when done.
		t.mu.Lock()
		t.removeWatcher(p, w)
		t.mu.Unlock()
		// Signal to the closing tree that we're done.
		t.watcherWG.Done()
	}

	if sequence == upspin.WatchCurrent {
		// Send the current state first. We must flush the tree so we
		// know our logs are current (or we need to recover the tree
		// from the logs).
		err := t.flush()
		if err != nil {
			return nil, err
		}

		// Make a copy of the tree so we have an immutable tree in
		// memory, at a fixed log position.
		offset := t.user.AppendOffset()
		clonedUser, err := t.user.ReadOnlyClone()
		if err != nil {
			return nil, err
		}
		clone := &Tree{
			user:     clonedUser,
			config:   t.config,
			packer:   t.packer,
			shutdown: make(chan struct{}),
			// there are no watchers on the clone.
		}

		// Start sending the current state of the cloned tree and setup
		// the watcher for this tree once the current state is sent.
		t.watcherWG.Add(1)
		go w.sendCurrentAndWatch(clone, t, p, offset)
	} else {
		var (
			offset    int64
			offsetErr error // Will be delivered on the channel, not here.
		)
		if sequence == upspin.WatchNew {
			// We must flush the tree so we know our logs are current (or we
			// need to recover the tree from the logs).
			err := t.flush()
			if err != nil {
				return nil, err
			}

			offset = t.user.AppendOffset()
		} else {
			offset = t.user.OffsetOf(sequence)
			if offset < 0 {
				offsetErr = errors.E(errors.Invalid, p.Path(), errors.Errorf("unknown sequence %d", sequence))
			}
		}

		// Set up the notification hook.
		t.addWatcher(p, w)

		// Start the watcher.
		t.watcherWG.Add(1)
		go w.watch(offset, offsetErr)
	}

	return w.events, nil
}

// addWatcher adds a watcher at the given path.
// t.mu must be held.
func (t *Tree) addWatcher(p path.Parsed, w *watcher) {
	name := p.Path()
	t.watchers[name] = append(t.watchers[name], w)
}

// removeWatcher removes the given watcher from the given path.
// t.mu must be held.
func (t *Tree) removeWatcher(p path.Parsed, w *watcher) {
	name := p.Path()
	ws := t.watchers[name]
	for i := range ws {
		if ws[i] == w {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(t.watchers, name)
	} else {
		t.watchers[name] = ws
	}
}

// notifyWatchers wakes any watchers that are watching the given path (or any
// of its ancestors).
// t.mu must be held.
func (t *Tree) notifyWatchers(name upspin.PathName) {
	p, _ := path.Parse(name)
	for {
		ws := t.watchers[p.Path()]
		for _, w := range ws {
			select {
			case w.hasWork <- true:
			default:
			}
		}
		if p.IsRoot() {
			break
		}
		p = p.Drop(1)
	}
}

// sendCurrentAndWatch takes an original tree and its clone and sends the state
// of the clone starting from the subtree rooted at p. The offset refers to the
// last log offset saved by the original tree. When sendCurrentAndWatch returns,
// the watcher and the cloned tree are closed.
// It must run in a goroutine. Errors are logged.
func (w *watcher) sendCurrentAndWatch(clone, orig *Tree, p path.Parsed, offset int64) {
	defer clone.Close()

	n, err := clone.loadPath(p)
	if err != nil && !errors.Is(errors.NotExist, err) {
		w.sendError(err)
		w.close()
		return
	}
	// If p exists, traverse the sub-tree and send its current state on the
	// events channel.
	if err == nil {
		fn := func(n *node, level int) error {
			logEntry := &serverlog.Entry{
				Op:    serverlog.Put,
				Entry: n.entry,
			}
			err := w.sendEvent(logEntry, offset)
			if err == errTimeout || err == errClosed {
				return nil
			}
			return err
		}
		err = clone.traverse(n, 0, fn)
		if err != nil {
			w.sendError(err)
			w.close()
			return
		}
	}
	// Set up the notification hook on the original tree. We must lock it.
	orig.mu.Lock()
	orig.addWatcher(p, w)
	orig.mu.Unlock()
	// Start the watcher (in this goroutine -- don't start a new one here).
	w.watch(offset, nil)
}

// sendEvent sends a single logEntry read from the log at offset position
// to the event channel. If the channel blocks for longer than watcherTimeout,
// the operation fails and the watcher is invalidated (marked for deletion).
func (w *watcher) sendEvent(logEntry *serverlog.Entry, offset int64) error {
	var event *upspin.Event
	// Strip block information for directories. We avoid an extra copy
	// if it's not a directory.
	if logEntry.Entry.IsDir() {
		entry := logEntry.Entry
		entry.MarkIncomplete()
		event = &upspin.Event{
			Entry:  &entry, // already a copy.
			Delete: logEntry.Op == serverlog.Delete,
		}
	} else {
		event = &upspin.Event{
			Entry:  &logEntry.Entry, // already a copy.
			Delete: logEntry.Op == serverlog.Delete,
		}
	}
	timer := time.NewTimer(watcherTimeout)
	defer timer.Stop()
	select {
	case <-w.shutdown:
		return errClosed
	case <-w.done:
		// Client is done receiving events.
		return errClosed
	case w.events <- event:
		// Event was sent.
		return nil
	case <-timer.C:
		// Oops. Client didn't read fast enough.
		return errTimeout
	}
}

func (w *watcher) sendError(err error) {
	e := &upspin.Event{
		Error: err,
	}
	select {
	case w.events <- e:
		// Error event was sent.
	case <-time.After(3 * watcherTimeout):
		// Can't send another error since we timed out again. Log an
		// error and close the watcher.
		log.Error.Printf("dir/server/tree.sendError: %s", errTimeout)
	}
}

// sendEventFromLog sends notifications to the given watcher for all
// descendant entries of a target path, reading from the given log starting at a
// given offset until it reaches the end of the log. It returns the next offset
// to read.
func (w *watcher) sendEventFromLog(offset int64) (int64, error) {
	curr := offset
	for {
		// Is the receiver still interested in reading events and the
		// tree still open for business?
		select {
		case <-w.done:
			return 0, errClosed
		case <-w.shutdown:
			return 0, errClosed
		default:
		}

		logEntry, next, err := w.log.ReadAt(curr)
		if err != nil {
			return next, errors.E(errors.Invalid, errors.Errorf("cannot read log at offset %d: %v", curr, err))
		}
		if next == curr {
			return curr, nil
		}
		curr = next
		path := logEntry.Entry.Name
		if !isPrefixPath(path, w.path) {
			// Not a log of interest.
			continue
		}
		err = w.sendEvent(&logEntry, curr)
		if err != nil {
			return 0, err
		}
	}
}

// watch, which runs in a goroutine, reads from the log starting at a given
// offset and sends notifications on the event channel until the end of the log
// is reached. It waits to be notified of more work or until the client's
// done channel is closed, in which case it terminates.
// The API for the DirServer.Watch requires that an invalid sequence
// is returned on the channel, not in the call. The initialErr argument here
// is present for that case: If non-nil, we deliver the error and stop.
// Otherwise if offset is negative, the first event will be an Invalid error.
func (w *watcher) watch(offset int64, initialErr error) {
	defer w.close()

	if initialErr != nil {
		w.sendError(initialErr)
		return
	}

	for {
		var err error
		offset, err = w.sendEventFromLog(offset)
		if err != nil {
			if err != errTimeout && err != errClosed {
				log.Debug.Printf("watch: sending error to client: %s", err)
				w.sendError(err)
			}
			return
		}
		select {
		case <-w.done:
			// Done channel was closed. Close watcher and quit this
			// goroutine.
			return
		case <-w.shutdown:
			// Tree has closed, nothing else to do.
			return
		case <-w.hasWork:
			// Wake up and work from where we left off.
		}
	}
}

// isClosed reports whether this watcher has been closed.
func (w *watcher) isClosed() bool {
	return atomic.LoadInt32(&w.closed) == 1
}

// close closes the watcher. Must only be called internally by the watcher's
// goroutine.
func (w *watcher) close() {
	atomic.StoreInt32(&w.closed, 1)
	close(w.events)
	w.doneFunc()
}

// isPrefixPath reports whether the path has a pathwise prefix.
func isPrefixPath(name upspin.PathName, prefix path.Parsed) bool {
	parsed, err := path.Parse(name)
	if err != nil {
		log.Debug.Print("dir/server/tree.isPrefixPath: error parsing path", name)
		return false
	}
	return parsed.HasPrefix(prefix)
}
