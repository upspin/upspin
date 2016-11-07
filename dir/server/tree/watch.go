// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"strings"
	"time"

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

// watcher holds together the done channel and the event channel for a given
// watch point.
type watcher struct {
	// The target path name this watcher watches.
	path upspin.PathName

	// ch is the Event channel with the client. It's write-only.
	ch chan *upspin.Event

	// done is the client's done channel. When it's closed, this watcher
	// dies. It's read-only.
	done <-chan struct{}

	// hasWork is an internal channel that the Tree uses to tell the watcher
	// to go look for work at the end of the log.
	hasWork chan bool

	// log is a cloned instance of the Tree's log that keeps track of this
	// watcher's progress.
	log *Log
}

// Watch implements upspin.DirServer.Watch.
func (t *Tree) Watch(p path.Parsed, order int64, done <-chan struct{}) (<-chan *upspin.Event, error) {
	const op = "dir/server/tree.Watch"
	const chanBufSize = 10 // TODO: what's the right buffer size?
	t.mu.Lock()
	defer t.mu.Unlock()

	// TODO: must we always flush here? I think not. Only in clone below.
	err := t.flush()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Clone the logs so we can read it while the current tree is updated
	// by others (we're about to unlock this tree and the log must not be
	// used concurrently).
	cLog, err := t.log.Clone()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Add the watcher from the current point on.
	// TODO: limit number of watchers on any given node.
	ch := make(chan *upspin.Event, chanBufSize)
	w := &watcher{
		path:    p.Path(),
		ch:      ch,
		done:    done,
		hasWork: make(chan bool, chanBufSize),
		log:     cLog,
	}

	if order == -1 {
		// Make a copy of the tree so we have an immutable tree in
		// memory, at a fixed log position.
		cIndex, err := t.logIndex.Clone()
		if err != nil {
			return nil, errors.E(op, err)
		}
		offset := t.log.LastOffset()
		clone := &Tree{
			user:     t.user,
			context:  t.context,
			packer:   t.packer,
			log:      cLog,
			logIndex: cIndex,
		}
		// Start sending the current state of the cloned tree and setup
		// the watcher for this tree once the current state is sent.
		go w.sendCurrentAndWatch(clone, t, p, offset)
	} else {
		// Setup the notification hook.
		err = t.addWatcher(p, w)
		if err != nil {
			return nil, errors.E(op, err)
		}

		// Start the watcher.
		go w.watch(order)
	}

	return w.ch, nil
}

// addWatcher adds a watcher to the node at a given path location.
// t.mu must be held.
func (t *Tree) addWatcher(p path.Parsed, w *watcher) error {
	n, _, err := t.loadPath(p)
	if err != nil {
		return err
	}
	n.watchers = append(n.watchers, w)
	return nil
}

// sendCurrentAndWatch sends the current state of the cloned tree and when done
// sets the watcher on a path of the original tree starting at the offset the
// cloned tree is in. It must run in a goroutine. Errors are logged.
func (w *watcher) sendCurrentAndWatch(clone, orig *Tree, p path.Parsed, offset int64) {
	const op = "dir/server/tree.sendCurrentAndWatch"

	// Locking the clone is not strictly needed since it only exists
	// temporarily in one goroutine. But it's safer to do so and prevent
	// future issues.
	clone.mu.Lock()
	defer clone.mu.Unlock()

	n, _, err := clone.loadPath(p)
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
		return
	}
	fn := func(n *node, level int) error {
		logEntry := &LogEntry{
			Op:    Put,
			Entry: n.entry,
		}
		err = w.sendNotification(logEntry, offset)
		if err != nil {
			return err
		}
		return nil
	}
	err = clone.traverse(n, 0, fn)
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
		return
	}

	// Setup the notification hook on the original tree. We must lock it.
	orig.mu.Lock()
	err = orig.addWatcher(p, w)
	orig.mu.Unlock()
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
		return
	}
	// Start the watcher (in this goroutine -- don't call go here).
	w.watch(offset)
}

// sendNotification sends a single logEntry read from the log at offset position
// to the event channel. If the channel blocks for longer watcherTimeout, the
// operation fails and the watcher is invalidated (marked for deletion).
func (w *watcher) sendNotification(logEntry *LogEntry, offset int64) error {
	if w.ch == nil {
		return errors.E(errors.Internal, errors.Str("send on a nil event channel"))
	}
	var event *upspin.Event
	// Strip block information for directories. We avoid an extra copy
	// if it's not a directory.
	if logEntry.Entry.IsDir() {
		entry := logEntry.Entry
		entry.MarkIncomplete()
		event = &upspin.Event{
			Order:  offset,
			Delete: logEntry.Op == Delete,
			Entry:  &entry,
		}
	} else {
		event = &upspin.Event{
			Order:  offset,
			Delete: logEntry.Op == Delete,
			Entry:  &logEntry.Entry,
		}
	}
	select {
	case w.ch <- event:
		// Event was sent.
	case <-time.After(watcherTimeout):
		// Oops. Client didn't read fast enough.
		// Can't send an error since we timed out. Close the
		// channel and log an error.
		if w.ch != nil {
			// The watcher will be removed from the node's
			// watcher slice on the next operation on the Tree.
			close(w.ch)
			w.ch = nil
		}
		err := errors.Str("Watcher channel timed out")
		log.Error.Printf("dir/server/tree.sendNotifications: %s", err)
		return errors.E(errors.IO, err)
	}
	return nil
}

// sendNotificationFromLog sends notifications to the given watcher for all
// descendant entries of a target path, reading from the given log from a given
// offset 'from' until it reaches the end. If the watcher channel fills up
// and is blocked for longer than watcher timeout duration, the operation is
// aborted and the watcher is closed.
func (w *watcher) sendNotificationFromLog(from int64) (int64, error) {
	curr := from
	for {
		logs, next, err := w.log.ReadAt(1, curr)
		if err != nil {
			return next, err
		}
		if len(logs) != 1 {
			// End of log.
			return next, nil
		}
		curr = next
		logEntry := logs[0]
		path := logEntry.Entry.SignedName
		if !strings.HasPrefix(string(path), string(w.path)) {
			// Not a log of interest.
			continue
		}
		err = w.sendNotification(&logEntry, curr)
		if err != nil {
			return 0, err
		}
	}
}

// watch runs in a goroutine looking at the log starting at a given offset and
// sends notifications on the event channel until the end of the log. It then
// waits to be notified of more work or until the client's done channel is
// closed.
func (w *watcher) watch(offset int64) {
	for {
		var err error
		offset, err = w.sendNotificationFromLog(offset)
		if err != nil {
			log.Error.Printf("dir/server/tree.watch: %s", err)
			return
		}
		select {
		case <-w.done:
			return
		case <-w.hasWork:
			// Wake up and work.
		}
	}
}

// removeDeadWatchers removes all watchers on a node that have closed their done
// channel.
func removeDeadWatchers(n *node) {
	curr := 0
	for i := 0; i < len(n.watchers); i++ {
		doneCh := n.watchers[i].done
		// Any nil channel means this watcher is dead.
		closed := n.watchers[i].ch == nil || doneCh == nil
		if !closed {
			// If the done channel is ready, it's closed.
			select {
			case <-doneCh:
				closed = true
			default:
			}
		}
		if closed {
			// Remove this entry. If there are more, simply copy the
			// next one over the ith entry. Otherwise just shrink
			// the slice.
			if i > curr {
				n.watchers[curr] = n.watchers[i]
			}
			continue
		}
		curr++
	}
	n.watchers = n.watchers[:curr]
}

// notifyWatchers tells all watchers there are new entries in the log to be
// processed.
func notifyWatchers(watchers []*watcher) {
	for _, w := range watchers {
		select {
		case w.hasWork <- true:
			// OK, sent.
		default:
			// Something bad happened. Not much we can do. Closing
			// the channel here is racy since we close it in a
			// goroutine as well.
			log.Error.Printf("dir/server/tree: Watcher goroutine is too busy. Server overloaded?")
		}
	}
}
