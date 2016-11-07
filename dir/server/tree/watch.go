// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// TODOs:
// - The watcher "tails" the log, starting from a given order number. It is
//   done in a goroutine because the order can be very far from current state
//   and we don't want to block the caller until all such state is sent on the
//   Event channel. However, once the watcher has caught up with the current
//   state of the Tree, there's no longer a need for a goroutine or for reading
//   the log directly (and thus spend time in disk I/O, unmarshalling, etc). We
//   can simply note that the end of file was reached, quit the goroutine and
//   sent events as they come in. This requires some extra synchronization code
//   and ensuring that sending does not block the Tree (we can keep the
//   goroutine if we don't want to impose a short timeout on the channel).

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

var errTimeout = errors.E(errors.IO, errors.Str("channel operation timed out"))

// watcher holds together the done channel and the event channel for a given
// watch point.
type watcher struct {
	// The target path name this watcher watches.
	path upspin.PathName

	// ch is the Event channel with the client. It is write-only.
	ch chan *upspin.Event

	// done is the client's done channel. When it's closed, this watcher
	// dies.
	done <-chan struct{}

	// hasWork is an internal channel that the Tree uses to tell the watcher
	// goroutine to look for work at the end of the log.
	hasWork chan bool

	// log is a read-only cloned instance of the Tree's log that keeps track
	// of this watcher's progress.
	log *Log
}

// Watch implements upspin.DirServer.Watch.
func (t *Tree) Watch(p path.Parsed, order int64, done <-chan struct{}) (<-chan *upspin.Event, error) {
	const op = "dir/server/tree.Watch"

	t.mu.Lock()
	defer t.mu.Unlock()

	// Clone the logs so we can keep reading it while the current tree
	// continues to be updated (we're about to unlock this tree).
	cLog, err := t.log.Clone()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Create a watcher, but do not attach it to any node yet.
	// TODO: limit number of watchers on any given node/tree?
	ch := make(chan *upspin.Event)
	w := &watcher{
		path:    p.Path(),
		ch:      ch,
		done:    done,
		hasWork: make(chan bool, 1),
		log:     cLog,
	}

	if order == -1 {
		// Send the current state first. We must flush the tree so we
		// know our logs are current (or we need to recover the tree
		// from the logs).
		err := t.flush()
		if err != nil {
			return nil, errors.E(op, err)
		}

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

// sendCurrentAndWatch sends the current state of the entry point p of cloned
// tree and when done sets the watcher on a path of the original tree starting
// at the offset the cloned tree is in. It must run in a goroutine. Errors are
// logged.
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
		err = w.sendEvent(logEntry, offset)
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

// sendEvent sends a single logEntry read from the log at offset position
// to the event channel. If the channel blocks for longer watcherTimeout, the
// operation fails and the watcher is invalidated (marked for deletion).
func (w *watcher) sendEvent(logEntry *LogEntry, offset int64) error {
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
		log.Error.Printf("dir/server/tree.sendNotifications: %s", errTimeout)
		return errTimeout
	}
	return nil
}

// sendEventFromLog sends notifications to the given watcher for all
// descendant entries of a target path, reading from the given log starting at a
// given offset until it reaches the end of the log. It returns the next offset
// to read.
func (w *watcher) sendEventFromLog(offset int64) (int64, error) {
	curr := offset
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
func (w *watcher) watch(offset int64) {
	for {
		var err error
		offset, err = w.sendEventFromLog(offset)
		if err != nil {
			log.Error.Printf("dir/server/tree.watch: %s", err)
			if err != errTimeout {
				w.ch <- &upspin.Event{
					Error: err,
				}
			}
			return
		}
		select {
		case <-w.done:
			// Done channel was closed. Quit this goroutine.
			if w.ch != nil {
				close(w.ch)
			}
			return
		case <-w.hasWork:
			// Wake up and work from where we left off.
		}
	}
}

// removeDeadWatchers removes all watchers on a node that have closed their done
// or Event channels.
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
			// Watcher is busy. It will get to it eventually.
		}
	}
}
