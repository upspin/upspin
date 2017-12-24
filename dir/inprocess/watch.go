// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

import (
	"time"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const watchTimeout = 10 * time.Second

// A listener connects the event manager to an events channel on which
// to deliver events to a single client.
type listener struct {
	eventMgr *eventManager
	root     path.Parsed     // The root of the subtree of interest.
	server   *server         // Holds the user info; needed for access control.
	done     <-chan struct{} // From the Watch method; signals termination.
	events   chan<- upspin.Event
	sequence int64 // The point in the event stream the listener has reached.
}

// want reports whether this listener is interested in the event, which means that the
// event is below its root in the tree and is accessible to its user. If the user does not
// have read permission, the event is trimmed and marked  incomplete.
func (l *listener) want(event upspin.Event, parsed path.Parsed) (upspin.Event, bool) {
	if !parsed.HasPrefix(l.root) {
		return upspin.Event{}, false
	}
	// Errors in access checks don't matter here; we just ignore things that fail or are blocked.
	if canAny, _ := l.server.can(access.AnyRight, parsed); !canAny {
		return upspin.Event{}, false
	}
	canRead, _ := l.server.can(access.Read, parsed)

	if event.Entry.IsDir() || (!canRead && !access.IsAccessControlFile(event.Entry.SignedName)) {
		// Must make a copy of the entry before cleaning it.
		entry := *event.Entry
		entry.MarkIncomplete()
		event.Entry = &entry
	}
	return event, true
}

func (l *listener) doneHandler() {
	<-l.done
	l.eventMgr.listenerDone <- l
}

// sendAll sends, if possible, all the events. It returns false if it cannot
// complete the list. l.sequence keeps track of our progress.
func (l *listener) sendAll(events []upspin.Event) bool {
	for _, event := range events {
		parsed, err := path.Parse(event.Entry.Name)
		if err != nil {
			// Shouldn't happen.
			log.Info.Printf("dir/inprocess.Sendall: parse error for %q: %v", event.Entry.Name, err)
			continue
		}
		if cleanEvent, ok := l.want(event, parsed); ok {
			select {
			case l.events <- cleanEvent:
				// Delivered.
				l.sequence++
			case <-time.After(watchTimeout):
				// Failed to deliver; client is not keeping up.
				return false
			}
		}
	}
	return true
}

// sendTree sends events for the tree rooted at name. The boolean
// return reports whether it sent all valid info to the client. Thus a
// false return means this listener did not start properly, but it is
// not an error if name does not exist, as it may appear later.
// If it returns false, it attempts to send an error event unless the
// problem was an unresponsive client.
func (l *listener) sendTree(name upspin.PathName) bool {
	const op errors.Op = "dir/inprocess.Watch"
	parsed, err := path.Parse(name)
	if err != nil {
		// Shouldn't happen.
		log.Info.Printf("%s: parse error in sendTree for %q: %v", op, name, err)
		l.sendEvent(upspin.Event{Error: errors.E(op, err)})
		return false
	}
	entry, err := l.server.lookup(op, parsed, false)
	if err != nil {
		// Ignore error. The problem (existence, permission) might resolve later.
		return true
	}
	event, ok := l.want(upspin.Event{Entry: entry}, parsed)
	if !ok {
		return true
	}
	if !l.sendEvent(event) {
		return false
	}
	if !entry.IsDir() {
		return true
	}
	entries, err := l.server.listDir(entry.Name)
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if !l.sendTree(entry.Name) {
			return false
		}
	}
	return true
}

// sendEvent sends an event on the channel, or returns false if it cannot.
func (l *listener) sendEvent(event upspin.Event) bool {
	select {
	case l.events <- event:
		// Delivered.
		// Do not increment sequence: these events are not part of the standard stream.
		return true
	case <-time.After(watchTimeout):
		// Failed to deliver; client is not keeping up.
		return false
	}
}

// eventManager is the structure that delivers events to all the listeners.
type eventManager struct {
	events    []upspin.Event
	listeners []*listener

	// These channels mediate all access to the event manager.
	eventsSoFar  chan []upspin.Event
	newListener  chan *listener
	newEvent     chan upspin.Event
	listenerDone chan *listener
}

// newEventManager returns a new event manager. There is one per database.
func newEventManager() *eventManager {
	// The numbers here are arbitrary, but for the testing purposes of
	// this package should be enough to prevent blocking the client.
	// A more serious attempt would scale these dynamically as needed.
	em := &eventManager{
		newListener: make(chan *listener, 100),
		newEvent:    make(chan upspin.Event, 100),
		eventsSoFar: make(chan []upspin.Event),
	}
	go em.run()
	return em
}

// run is the goroutine managing the single event manager.
// All interaction with the event manager is through the channels handled
// in this goroutine, obviating explicit mutexes.
func (e *eventManager) run() {
	// Invariant: Each element of e.listeners has its sequence at the current point.
	// When we receive an event, each element of that list is ready for it.
	// We only add a new listener to the list once it has caught up to the rest.
	for {
		select {
		case e.eventsSoFar <- e.events:
			// Nothing to do.
		case listener := <-e.listenerDone:
			e.delete(listener)
		case listener := <-e.newListener:
			// New listener has been created, but it may be behind.
			if listener.sequence < int64(len(e.events)) && !listener.sendAll(e.events[listener.sequence:]) {
				// It couldn't catch up, so ignore it and don't install it.
				close(listener.events)
				continue
			}
			e.listeners = append(e.listeners, listener)
		case event := <-e.newEvent:
			parsed, err := path.Parse(event.Entry.Name)
			if err != nil {
				// Shouldn't happen.
				log.Info.Printf("dir/inprocess: parse error in event for %q: %v", event.Entry.Name, err)
				continue
			}
			n := len(e.listeners)
			for i := 0; i < n; i++ {
				l := e.listeners[i]
				if cleanEvent, ok := l.want(event, parsed); ok {
					if l.sendEvent(cleanEvent) {
						// Delivered.
						l.sequence++
					} else {
						// Failed to deliver; client is not keeping up.
						e.deleteNth(i)
						i-- // Back up the loop counter; ith guy is gone.
						n--
					}
				}
			}
			e.events = append(e.events, event)
		}
	}
}

// deleteNth deletes the Nth listener from the eventManager's list.
func (e *eventManager) deleteNth(i int) {
	close(e.listeners[i].events)
	copy(e.listeners[i:], e.listeners[i+1:])
	e.listeners = e.listeners[:len(e.listeners)-1]
}

// delete deletes the listener from the eventManager's list.
func (e *eventManager) delete(which *listener) {
	for i, l := range e.listeners {
		if l == which {
			e.deleteNth(i)
			return
		}
	}
	panic("listener not found in event loop")
}

// watch is the implementation of DirServer.Watch after basic checking is done.
func (e *eventManager) watch(server *server, root path.Parsed, sequence int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	const op errors.Op = "dir/inprocess.Watch"
	events := make(chan upspin.Event, 10)
	l := &listener{
		eventMgr: e,
		root:     root,
		server:   server,
		done:     done,
		events:   events,
		sequence: 0,
	}
	go l.doneHandler()

	eventsSoFar := <-e.eventsSoFar

	// A sequence other than the special cases must exist.
	// The special case of an invalid sequence is returned as an event with an "invalid" error.
	if sequence != upspin.WatchCurrent && sequence != upspin.WatchStart && sequence != upspin.WatchNew {
		if sequence < 0 || int64(len(eventsSoFar)) < sequence {
			events <- upspin.Event{Error: errors.E(op, errors.Invalid, "bad sequence")}
			close(events)
			return events, nil
		}
	}

	// Must do this in the background and return so client can receive initialization events.
	go func() {
		switch sequence {
		case upspin.WatchStart:
			// 0 is a special case in the API, but it's not a special case here.
			fallthrough
		default:
			if !l.sendAll(eventsSoFar) {
				log.Printf("dir/inprocess.Watch %q could not send all initial events", root)
				return
			}
		case upspin.WatchCurrent:
			// Send state of tree under name.
			if !l.sendTree(root.Path()) {
				log.Printf("dir/inprocess.Watch %q could not send all initial events", root)
				return
			}
			fallthrough
		case upspin.WatchNew:
			// Start transmitting from where we were before sendTree.
			l.sequence = int64(len(eventsSoFar))
		}
		e.newListener <- l
	}()

	return events, nil
}
