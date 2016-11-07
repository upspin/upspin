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
	standardWatcherTimeout   = 50 * time.Millisecond
	backgroundWatcherTimeout = 1 * time.Second
)

type watcher struct {
	ch   chan *upspin.Event
	done <-chan struct{}
}

// Watch implements upspin.DirServer.Watch.
func (t *Tree) Watch(p path.Parsed, order int64, done <-chan struct{}) (<-chan *upspin.Event, error) {
	const op = "dir/server/tree.Watch"

	t.mu.Lock()

	// TODO: must we always flush here?
	err := t.flush()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Add the watcher from the current point on.
	// TODO: limit number of watchers on any given node.
	ch := make(chan *upspin.Event, 10) // TODO: what's the right buffer size?
	watcher := watcher{
		ch:   ch,
		done: done,
	}

	// Clone the logs so we can read it while the current tree is updated
	// by others (we're about to unlock this tree and the log must not be
	// used concurrently).
	cLog, err := t.log.Clone()
	if err != nil {
		return nil, errors.E(op, err)
	}
	cIndex, err := t.logIndex.Clone()
	if err != nil {
		return nil, errors.E(op, err)
	}

	t.mu.Unlock()
	// The current tree is now possibly changing.

	if order == -1 {
		// TODO: clone the tree and traverse its transitive closure.
		clone := &Tree{
			user:     t.user,
			context:  t.context,
			packer:   t.packer,
			log:      cLog,
			logIndex: cIndex,
		}
		clone.Flush() // meh.
		// TODO: Traverse the transitive closure for the cloned tree.
	} else {
		// TODO: send updates from order until t.log.LastOffset.
	}

	// Lock this tree so we can atomically do the following: send updates
	// that happened while the tree was unlocked and establish the watcher
	// when no further updates remain.

	t.mu.Lock()
	defer t.mu.Unlock()

	// Now march the logs forward till t.log.LastOffset().

	// Setup the notification hook.
	n, _, err := t.loadPath(p)
	if err != nil {
		return nil, errors.E(op, err)
	}
	n.watchers = append(n.watchers, watcher)

	return watcher.ch, nil
}

// sendNotification sends a single logEntry at an order position to all listed
// watchers. If the client is not fast enough and the channel blocks for longer
// then the timeout, the operation silently fails (see TODO below).
func sendNotification(watchers []watcher, logEntry *LogEntry, offset int64, timeout time.Duration) {
	for _, watcher := range watchers {
		event := &upspin.Event{
			Order:  offset,
			Delete: logEntry.Op == Delete,
			Entry:  &logEntry.Entry,
		}
		select {
		case watcher.ch <- event:
			// All good. Event sent.
		case <-time.After(timeout):
			// Oops. Client didn't read fast enough.

			// TODO: the interface says to send an event with an
			// errTimeout error but if send is blocking, we can't
			// send an error either. Log an error for now.
			log.Error.Printf("dir/server/tree.sendWatcherNotifications: Watcher channel timed out")
			break
		}
	}
}

// sendNotificationFromLog sends notifications to the given watcher for all
// descendant entries of a target path, reading from the given log from a given
// offset 'from' until it reaches offset 'to'. If the watcher channel fills up
// and is blocked for longer than the timeout duration, the operation is
// aborted.
func sendNotificationFromLog(target upspin.PathName, log *Log, w watcher, from int64, to int64, timeout time.Duration) error {
	curr := from
	for {
		logs, next, err := log.ReadAt(1, curr)
		if err != nil {
			return err
		}
		if len(logs) != 1 {
			return nil
		}
		logEntry := logs[0]
		path := logEntry.Entry.Name
		if !strings.HasPrefix(string(path), string(target)) {
			// Not a log of interest.
			continue
		}
		sendNotification([]watcher{w}, &logEntry, curr, backgroundWatcherTimeout)
		curr = next
	}
	return nil
}

// removeDeadWatchers removes all watchers on a node that have closed their done
// channel.
func removeDeadWatchers(n *node) {
	for i := 0; i < len(n.watchers); {
		done := n.watchers[i].done
		closed := false
		if done == nil {
			closed = true
		} else {
			select {
			case <-done:
				closed = true
			default:
			}
		}
		if closed {
			// Close the outgoing Event channel.
			if n.watchers[i].ch != nil {
				close(n.watchers[i].ch)
			}

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
