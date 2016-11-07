// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"strings"
	"time"

	"sync"
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
	hasWork chan struct{}

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
	w := watcher{
		ch:      ch,
		done:    done,
		hasWork: make(chan struct{}, chanBufSize),
		log:     cLog,
	}

	// DO THIS IN A GOROUTINE!
	if order == -1 {
		// Make a copy of the tree so we don't get further updates.
		cIndex, err := t.logIndex.Clone() // TODO: is this safe without t.mu held?
		if err != nil {
			return nil, errors.E(op, err)
		}
		clone := &Tree{
			user:     t.user,
			context:  t.context,
			packer:   t.packer,
			log:      cLog,
			logIndex: cIndex,
		}
		clone.Flush() // meh. remove!!!
		// TODO: Traverse the transitive closure for the cloned tree.
	}

	// Start watcher.
	w.watch(order)

	// Setup the notification hook.
	n, _, err := t.loadPath(p)
	if err != nil {
		return nil, errors.E(op, err)
	}
	n.watchers = append(n.watchers, w)

	return w.ch, nil
}

// sendNotification sends a single logEntry read from the log at offset position
// onto the event channel. If the channel receiver is not reading fast enough
// and the channel blocks for longer watcherTimeout, the operation fails
// and the watcher is invalidated.
func (w *watcher) sendNotification(logEntry *LogEntry, offset int64) error {
	if w.ch == nil {
		return errors.E(errors.Internal, errors.Str("send on a nil event channel"))
	}
	event := &upspin.Event{
		Order:  offset,
		Delete: logEntry.Op == Delete,
		Entry:  &logEntry.Entry,
	}
	select {
	case w.ch <- event:
		// Event was sent.
	case <-time.After(watcherTimeout):
		// Oops. Client didn't read fast enough.
		// Can't send an error since we timed out. Close the
		// channel and log an error.
		if watcher.ch != nil {
			// The watcher will be removed from the node's
			// watcher slice on the next operation on the Tree.
			close(watcher.ch)
			watcher.ch = nil
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
		logEntry := logs[0]
		path := logEntry.Entry.Name
		if !strings.HasPrefix(string(path), string(w.path)) {
			// Not a log of interest.
			continue
		}
		err = w.sendNotification(&logEntry, curr)
		if err != nil {
			return 0, err
		}
		curr = next
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
	for i := 0; i < len(n.watchers); {
		done := n.watchers[i].done
		// Any nil channel means this watcher is dead.
		closed := n.watchers[i].ch == nil || done == nil
		if !closed {
			// If the done channel is ready, it's closed.
			select {
			case <-done:
				closed = true
			default:
			}
		}
		if closed {
			// Remove this entry. If there are more, simply copy the
			// next one over the ith entry. Otherwise just shrink
			// the slice.
			if len(n.watchers) > i+1 {
				n.watchers[i] = n.watchers[i+1]
				n.watchers = n.watchers[:i+1]
			} else {
				n.watchers = n.watchers[:i]
			}
			continue
		}
		i++
	}
}

// notifyWatchers tells all watchers there are new entries in the log to be
// processed.
func notifyWatchers(watchers []*watcher) {
	for _, w := range watchers {
		w.hasWork <- true
	}
}
