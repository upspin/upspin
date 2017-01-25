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

type listener struct {
	eventMgr *eventManager
	server   *server
	done     <-chan struct{}
	events   chan<- upspin.Event
	order    int64
}

func (l *listener) want(event upspin.Event, parsed path.Parsed) (upspin.Event, bool) {
	// Errors in access checks don't matter here; we just ignore things that fail or are blocked.
	if canAny, _ := l.server.can(access.AnyRight, parsed); !canAny {
		return upspin.Event{}, false
	}
	if canRead, _ := l.server.can(access.Read, parsed); !canRead || event.Entry.IsDir() {
		// Must make a copy of the entry before cleaning it.
		entry := *event.Entry
		entry.MarkIncomplete()
		if entry.IsDir() {
			entry.Blocks = nil
		}
		event.Entry = &entry
	}
	return event, true
}

func (l *listener) doneHandler() {
	<-l.done
	l.eventMgr.listenerDone <- l
}

func (l *listener) sendAll(events []upspin.Event) bool {
	for _, event := range events {
		parsed, err := path.Parse(event.Entry.Name)
		if err != nil {
			// Shouldn't happen.
			log.Info.Printf("parse error in event for %q: %v", event.Entry.Name, err)
			continue
		}
		if cleanEvent, ok := l.want(event, parsed); ok {
			select {
			case l.events <- cleanEvent:
				// Delivered.
			case <-time.After(watchTimeout):
				// Failed to deliver; client is not keeping up.
				return false
			}
		}
	}
	return true
}

// sendTree sends events for the tree rooted at name. The boolean
// return reports whether it sent all valid info to the client. If there
// is an error other than failing to send, it returns true.
func (l *listener) sendTree(name upspin.PathName) bool {
	const op = "dir/inprocess.Watch"
	parsed, err := path.Parse(name)
	if err != nil {
		// Cannot happen.
		panic("parse error in sendTree")
	}
	entry, err := l.server.lookup(op, parsed, false)
	if err != nil {
		return true
	}
	event, ok := l.want(upspin.Event{Entry: entry}, parsed)
	if !ok {
		return true
	}
	select {
	case l.events <- event:
		// Delivered.
	case <-time.After(watchTimeout):
		// Failed to deliver; client is not keeping up.
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
		l.sendTree(entry.Name)
	}
	return true
}

type eventManager struct {
	db        *database
	events    []upspin.Event
	listeners []*listener

	eventsSoFar  chan []upspin.Event
	newListener  chan *listener
	newEvent     chan upspin.Event
	listenerDone chan *listener
}

var eventMgr = &eventManager{
	newListener: make(chan *listener, 100),
	newEvent:    make(chan upspin.Event, 100),
	eventsSoFar: make(chan []upspin.Event),
}

func init() {
	go eventMgr.run()
}

func (e *eventManager) run() {
	for {
		select {
		case e.eventsSoFar <- e.events:
			// Nothing to do.
		case listener := <-e.listenerDone:
			e.delete(listener)
		case listener := <-e.newListener:
			// New listener has been created, but it may be behind.
			if listener.order < int64(len(e.events)) && !listener.sendAll(e.events[listener.order:]) {
				// It couldn't catch up, so ignore it and don't install it.
				close(listener.events)
				continue
			}
			e.listeners = append(e.listeners, listener)
		case event := <-e.newEvent:
			parsed, err := path.Parse(event.Entry.Name)
			if err != nil {
				// Shouldn't happen.
				log.Info.Printf("parse error in event for %q: %v", event.Entry.Name, err)
				continue
			}
			n := len(e.listeners)
			for i := 0; i < n; i++ {
				l := e.listeners[i]
				if cleanEvent, ok := l.want(event, parsed); ok {
					select {
					case l.events <- cleanEvent:
						// Delivered.
					case <-time.After(watchTimeout):
						// Failed to deliver; client is not keeping up.
						e.deleteNth(i)
						i--
						n--
					}
				}
			}
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
func (e *eventManager) watch(server *server, name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	const op = "dir/inprocess.Watch"
	events := make(chan upspin.Event, 10)
	l := &listener{
		eventMgr: eventMgr,
		server:   server,
		done:     done,
		events:   events,
		// order is set below.
	}
	go l.doneHandler()

	var eventsToSend []upspin.Event

	eventsSoFar := <-e.eventsSoFar

	// An order other than the special cases 0 and 1 must exist.
	// The special case of an invalid order is returned as an event with an "invalid" error.
	if order != 0 && order != -1 {
		if order < 0 || int64(len(eventsSoFar)) <= order {
			events <- upspin.Event{Error: errors.E(op, errors.Invalid, errors.Str("bad order"))}
			close(events)
			return events, nil
		}
	}

	l.order = int64(len(eventsSoFar)) // After initialization, this is where we will be.

	// Must do this the background and return to client so it can receive initialization events.
	go func() {
		switch order {
		default:
			fallthrough
		case 0:
			// 0 is a special case in the API, but it's not a special case here.
			if !l.sendAll(eventsSoFar) {
				log.Printf("Watch %q could not send all initial events", name)
				return
			}
		case -1:
			// Send state of tree under name.
			if !l.sendTree(name) {
				log.Printf("Watch %q could not send all initial events", name)
				return
			}
		}

		if !l.sendAll(eventsToSend) {
			log.Printf("Watch %q could not send all initial events", name)
			return
		}
		e.newListener <- l
	}()

	return events, nil
}
