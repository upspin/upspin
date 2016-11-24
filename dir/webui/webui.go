// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package webui implements a DirServer wrapper that exports a simple web UI
// that publishes all public files hosted by the wrapped DirServer.
package webui

import (
	"net/http"
	"reflect"
	"sync"

	"upspin.io/access"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

type Server struct {
	upspin.DirServer

	wakeUp chan bool

	mu    sync.Mutex
	trees []*tree
}

type tree struct {
	mu sync.RWMutex

	done   chan struct{}
	events <-chan upspin.Event
	root   *node
}

type node struct {
	name string
	kids []*node
}

// WrapDir wraps an upspin.DirServer and exposes a webui HTTP handler that
// navigates all publicly-readable paths served by the DirServer. It must be
// given a slide of pre-existing roots. It will discover new roots as they are
// created and forgot them when they are deleted.
func WrapDir(dir upspin.DirServer, availableRoots []upspin.PathName) (*Server, error) {
	const op = "dir/webui.WrapDir"
	ctx := context.New()
	ctx = context.SetUserName(ctx, access.AllUsers)
	ctx = context.SetDirEndpoint(ctx, dir.Endpoint())

	log.Printf("=== dialing endpoint: %v", dir.Endpoint().NetAddr)
	service, err := dir.Dial(ctx, dir.Endpoint())
	if err != nil {
		return nil, errors.E(op, err)
	}
	newDir, ok := service.(upspin.DirServer)
	if !ok {
		return nil, errors.E(op, errors.Str("not a DirServer implementation"))
	}

	s := &Server{
		DirServer: newDir,
		wakeUp:    make(chan bool, 1),
	}

	go s.watch()

	// Start a watcher for each available root.
	for _, r := range availableRoots {
		p, err := path.Parse(r)
		if err != nil {
			return nil, errors.E(op, err)
		}
		err = s.watchRoot(p)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}
	return s, nil
}

func (s *Server) watchRoot(p path.Parsed) error {
	log.Printf("=== going to watch: %s", p)
	if !p.IsRoot() {
		return errors.E(errors.Internal, p.Path(), errors.Str("must be root"))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Does root already exist?
	for _, r := range s.trees {
		r.mu.RLock()
		if r.root.name == p.String() {
			// Root already watched. TODO: error?
			r.mu.RUnlock()
			return nil
		}
		r.mu.RUnlock()
	}

	// Add new tree.
	tree := &tree{
		done: make(chan struct{}),
		root: &node{
			name: string(p.User()),
		},
	}
	s.trees = append(s.trees, tree)

	var err error
	tree.events, err = s.Watch(p.Path(), -1, tree.done)
	s.wakeUp <- true // tell watch goroutine there's something to do.
	return err
}

func (s *Server) unwatchRoot(name upspin.PathName) error {
	// TODO:
	return nil
}

func (s *Server) selectCasesFromRoots() []reflect.SelectCase {
	s.mu.Lock()
	defer s.mu.Unlock()
	cases := make([]reflect.SelectCase, len(s.trees)+1)
	// The wakeup channel is case zero.
	cases[0] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(s.wakeUp)}
	for i, root := range s.trees {
		log.Printf("=== adding tree number: %d", i+1)
		root.mu.Lock()
		cases[i+1] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(root.events)}
		root.mu.Unlock()
	}
	return cases
}

// ..... runs in a goroutine.
func (s *Server) watch() {
	cases := s.selectCasesFromRoots()
	for {
		// Wait to be notified of a new event.
		log.Printf("==== waiting to be notified of shit to do")
		chosen, value, ok := reflect.Select(cases)
		if !ok {
			// TODO: remove chosen channel from set of roots.
			log.Printf("=== got non-okay call on chan %d", chosen)
			continue
		}
		if chosen == 0 {
			log.Printf("=== got wakeup call!")
			// We got a wakeup call. Re-generate cases and restart
			// the loop.
			cases = s.selectCasesFromRoots()
			continue
		}
		event, ok := value.Interface().(upspin.Event)
		if !ok {
			// Value sent by the server is not an Event. Maybe it's
			// nil?
			log.Error.Printf("dir/webui: got invalid event type: %v", event)
			// TODO: remove this root?
			continue
		}
		log.Printf("Processing event %v, chosen: %d", event, chosen)
		s.mu.Lock()
		root := s.trees[chosen-1]
		s.mu.Unlock()

		// Now do something with this event.
		// TODO: run a goroutine pool so we don't miss events from the
		// server? Don't run in its own goroutine because of fan-out
		// issues.
		s.process(&event, root)
	}
}

// process processes the Event by adding or removing entries to/from the set of
// nodes we keep track of.
func (s *Server) process(e *upspin.Event, tree *tree) {
	const op = "dir/webui.process"
	if e.Error != nil {
		// TODO: what else can we do?
		log.Error.Printf("%s: event error: %s", op, e.Error)
		return
	}
	p, err := path.Parse(e.Entry.Name)
	if err != nil {
		// This can't happen.
		log.Error.Printf("%s: %s", op, err)
		return
	}
	// Loop over the path an insert or remove entries as needed.
	if e.Delete {
		log.Printf("== got delete event: %s", p)
		s.deletePath(p, tree)
	} else {
		log.Printf("== got add event: %s", p)
		s.addPath(p, tree)
	}
}

func (s *Server) deletePath(p path.Parsed, tree *tree) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	s.deletePathLocked(p, tree)
}

func (s *Server) deletePathLocked(p path.Parsed, tree *tree) {
	n := tree.root
	if n.name != string(p.User()) {
		log.Error.Printf("should never happen")
		return
	}
	if p.IsRoot() {
		log.Error.Printf("can't delete root.")
		return
	}

Outer:
	for i := 0; i < p.NElem(); i++ {
		elem := p.Elem(i)
		delete := i == p.NElem()-1
		for j := 0; j < len(n.kids); j++ {
			if elem == n.kids[j].name {
				if delete {
					// Remove the jth kid.
					if j == len(n.kids)-1 {
						n.kids = n.kids[:j]
					} else {
						n.kids = append(n.kids[:j], n.kids[j+1:]...)
					}
					// If the node is now empty, remove it
					// from the parent.
					if len(n.kids) == 0 && i > 0 {
						s.deletePathLocked(p.Drop(1), tree)
					}
					break Outer
				} else {
					n = n.kids[j]
					continue Outer
				}
			}
		}
		log.Error.Printf("not found element %q, ancestor to deletion target %q", elem, p.Path())
		return
	}
}

func (s *Server) addPath(p path.Parsed, tree *tree) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	n := tree.root
	if n.name != string(p.User()) {
		log.Error.Printf("should never happen")
		return
	}
Outer:
	for i := 0; i < p.NElem(); i++ {
		elem := p.Elem(i)
		for j := 0; j < len(n.kids); j++ {
			if elem == n.kids[j].name {
				n = n.kids[j]
				continue Outer
			}
		}
		log.Debug.Printf("elem %q not found; creating now", elem)
		// Create node for it then.
		newNode := &node{
			name: elem,
		}
		n.kids = append(n.kids, newNode)
		n = newNode
	}
}

// TODO: Override Put looking for new roots being added. No need to do the
// converse with Delete because we get Delete events from the Events channel.

// ServeHTTP implements net.http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {

}
