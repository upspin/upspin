// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command demoserver serves an Upspin tree containing a series of boxes
// (files) containing Schrödinger's cats. The cats inside the boxes are in the
// superposition of dead and alive until a client does a Lookup of the box, at
// which point the superposition collapses and the reality of the cat's state
// is revealed.
//
// The purpose of this program is to demonstrate the implementation of a
// combined Upspin DirServer and StoreServer that serves dynamic content.
//
// See also: https://en.wikipedia.org/wiki/Schrödinger's_cat
package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil"
	"upspin.io/upspin"

	_ "upspin.io/key/transports"
	_ "upspin.io/pack/eeintegrity"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	flags.Parse(flags.Server)

	addr := upspin.NetAddr(flags.NetAddr)
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   addr,
	}
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	s, err := newServer(ep, cfg)
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/api/Store/", storeserver.New(cfg, s.StoreServer(), addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, s.DirServer(), addr))

	https.ListenAndServeFromFlags(nil, "demoserver")
}

// box represents an opened box.
type box struct {
	*upspin.DirEntry
	data []byte
}

// server provides implementations of upspin.DirServer and upspin.StoreServer
// (accessed by calling the respective methods) that serve a tree containing
// many boxes containing Schrödinger's Cats.
type server struct {
	ep  upspin.Endpoint
	cfg upspin.Config

	accessEntry *upspin.DirEntry
	accessBytes []byte

	mu    sync.Mutex
	open  *sync.Cond // Broadcast when a box is opened for the first time.
	boxes []box
}

type dirServer struct {
	*server
}

type storeServer struct {
	*server
}

func (s *server) DirServer() upspin.DirServer {
	return &dirServer{s}
}

func (s *server) StoreServer() upspin.StoreServer {
	return &storeServer{s}
}

const (
	accessRef  = upspin.Reference(access.AccessFile)
	accessFile = "read,list:all\n"
)

var accessRefdata = upspin.Refdata{Reference: accessRef}

func newServer(ep upspin.Endpoint, cfg upspin.Config) (*server, error) {
	s := &server{
		ep:  ep,
		cfg: cfg,
	}
	s.open = sync.NewCond(&s.mu)

	var err error
	s.accessEntry, s.accessBytes, err = s.pack(access.AccessFile, []byte(accessFile))
	if err != nil {
		return nil, err
	}

	return s, nil
}

const packing = upspin.EEIntegrityPack

func (s *server) pack(filePath string, data []byte) (*upspin.DirEntry, []byte, error) {
	name := upspin.PathName(s.cfg.UserName()) + "/" + upspin.PathName(filePath)
	de := &upspin.DirEntry{
		Writer:     s.cfg.UserName(),
		Name:       name,
		SignedName: name,
		Packing:    packing,
		Time:       upspin.Now(),
		Sequence:   1,
	}

	bp, err := pack.Lookup(packing).Pack(s.cfg, de)
	if err != nil {
		return nil, nil, err
	}
	cipher, err := bp.Pack(data)
	if err != nil {
		return nil, nil, err
	}
	bp.SetLocation(upspin.Location{
		Endpoint:  s.ep,
		Reference: upspin.Reference(filePath),
	})
	return de, cipher, bp.Close()
}

// These methods implement upspin.Service.

func (s *server) Endpoint() upspin.Endpoint { return s.ep }
func (*server) Ping() bool                  { return true }
func (*server) Close()                      {}

// These methods implement upspin.Dialer.

func (s *storeServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) { return s, nil }
func (s *dirServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error)   { return s, nil }

// These methods implement upspin.DirServer.

func (s *dirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}

	fp := p.FilePath()
	switch fp {
	case "": // Root directory.
		return &upspin.DirEntry{
			Name:       p.Path(),
			SignedName: p.Path(),
			Attr:       upspin.AttrDirectory,
			Time:       upspin.Now(),
		}, nil
	case access.AccessFile:
		return s.accessEntry, nil
	}

	n := matchBox(fp)
	if n < 0 {
		return nil, errors.E(name, errors.NotExist)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	total := len(s.boxes)
	if n > total {
		return nil, errors.E(name, errors.NotExist)
	}

	if n == total {
		// A new box is opened!
		de, data, err := s.pack(fp, randomState())
		if err != nil {
			return nil, errors.E(name, err)
		}
		s.boxes = append(s.boxes, box{de, data})
		s.open.Broadcast()
	}

	return s.boxes[n].DirEntry, nil
}

func (s *dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return serverutil.Glob(pattern, s.Lookup, s.listDir)
}

func (s *dirServer) listDir(name upspin.PathName) ([]*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	if p.User() != s.cfg.UserName() || p.FilePath() != "" {
		return nil, errors.E(name, errors.NotExist)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var des []*upspin.DirEntry

	// List all the opened boxes in numerical order.
	for n := range s.boxes {
		de := s.boxes[n].DirEntry.Copy()
		de.MarkIncomplete()
		des = append(des, de)
	}

	// The final, closed box.
	des = append(des, s.closedBox(len(s.boxes)))

	return des, nil
}

func (s *dirServer) closedBox(n int) *upspin.DirEntry {
	name := upspin.PathName(s.cfg.UserName()) + "/" + upspin.PathName(fmtBox(n))
	return &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Attr:       upspin.AttrIncomplete,
		Time:       upspin.Now(),
		Writer:     s.cfg.UserName(),
	}
}

func (s *dirServer) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	if p.User() != s.cfg.UserName() {
		return nil, errors.E(name, errors.NotExist)
	}

	fp := p.FilePath()
	match := func(de *upspin.DirEntry) bool {
		return fp == "" || name == de.Name

	}

	n := int(order)
	if n < 0 {
		n = 0
	}
	isDone := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	events := make(chan upspin.Event)
	go func() {
		<-done
		s.open.Broadcast()
	}()
	go func() {
		defer close(events)
		for {
			s.mu.Lock()
			if n == len(s.boxes) {
				// Send the closed box.
				go func(n int) {
					if de := s.closedBox(n); match(de) {
						events <- upspin.Event{
							Entry: s.closedBox(n),
							Order: int64(n),
						}
					}
				}(n)
			}
			for !isDone() && n >= len(s.boxes) {
				s.open.Wait()
			}
			if isDone() {
				s.mu.Unlock()
				return
			}
			de := s.boxes[n].DirEntry
			s.mu.Unlock()

			// Send the next opened box.
			if match(de) {
				events <- upspin.Event{
					Entry: de,
					Order: int64(n),
				}
			}
			n++
		}
	}()
	return events, nil
}

func (s *dirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return s.accessEntry, nil
}

// This method implements upspin.StoreServer.

func (s *storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if ref == accessRef {
		return s.accessBytes, &accessRefdata, nil, nil
	}

	n := matchBox(string(ref))

	s.mu.Lock()
	defer s.mu.Unlock()

	if n < 0 || n >= len(s.boxes) {
		return nil, nil, nil, errors.E(errors.NotExist, errors.Errorf("unknown reference %q", ref))
	}

	return s.boxes[n].data, &upspin.Refdata{Reference: ref}, nil, nil
}

// The DirServer and StoreServer methods below are not implemented.

var errNotImplemented = errors.E(errors.Permission, errors.Str("method not implemented: dingus is read-only"))

func (*dirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (*dirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (*storeServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, errNotImplemented
}

func (*storeServer) Delete(ref upspin.Reference) error {
	return errNotImplemented
}

// Utility functions.

const boxName = "box"

func fmtBox(n int) string {
	return fmt.Sprintf("%s%d", boxName, n)
}

func matchBox(filePath string) int {
	if !strings.HasPrefix(filePath, boxName) {
		return -1
	}
	n := filePath[len(boxName):]
	i, _ := strconv.ParseInt(n, 10, 32)
	if i < 0 || fmtBox(int(i)) != filePath {
		return -1
	}
	return int(i)
}

var states = [][]byte{
	[]byte("A dead cat.\n"),
	[]byte("A live cat.\n"),
}

func randomState() []byte {
	return states[rand.Intn(2)]
}
