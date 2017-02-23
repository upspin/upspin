// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package filesystem provides an upspin.StoreServer
// that serves files from a local file system.
// It must be used in conjunction with the upspin.DirServer
// implementation in package upspin.io/dir/filesystem.
package filesystem

import (
	"time"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// New creates a new StoreServer instance with the
// provided server configuration and  options.
// The only valid configuration option is "root", which
// specifies a path to the file system root.
func New(cfg upspin.Config, options ...string) (upspin.StoreServer, error) {
	const op = "store/filesystem.New"

	s := &server{server: cfg}

	var err error
	s.root, s.defaultAccess, err = newRoot(cfg, options)
	if err != nil {
		return nil, errors.E(op, err)
	}

	return s, nil
}

type server struct {
	// Set by New.
	root          string
	server        upspin.Config
	defaultAccess *access.Access

	// Set by Dial.
	user upspin.Config
}

var errNotDialed = errors.E(errors.Internal, errors.Str("must Dial before making request"))

func (s *server) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op = "store/filesystem.Get"
	log.Debug.Println(op, ref)

	if s.user == nil {
		return nil, nil, nil, errors.E(op, errNotDialed)
	}

	pathName := upspin.PathName(s.server.UserName()) + "/" + upspin.PathName(ref)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil, nil, errors.E(op, err)
	}

	// Verify that the requesting user can access this file.
	if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.Read, parsed); err != nil {
		return nil, nil, nil, errors.E(op, err)
	} else if !ok {
		return nil, nil, nil, errors.E(op, parsed.Path(), access.ErrPermissionDenied)
	}

	data, err := readFile(s.root, pathName)
	if err != nil {
		return nil, nil, nil, errors.E(op, err)
	}
	refdata := &upspin.Refdata{
		Reference: ref,
		Volatile:  false,
		Duration:  time.Minute, // TODO: Just for fun.
	}
	return data, refdata, nil, nil
}

func (s *server) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/filesystem.Dial"

	dialed := *s
	dialed.user = cfg
	return &dialed, nil
}

func (s *server) Ping() bool {
	return true
}

func (s *server) Close() {
}

// Methods that are not implemented.

var errNotImplemented = errors.Str("not implemented")

func (s *server) Put(ciphertext []byte) (*upspin.Refdata, error) {
	const op = "store/filesystem.Put"
	return nil, errors.E(op, errNotImplemented)
}

func (s *server) Delete(ref upspin.Reference) error {
	const op = "store/filesystem.Delete"
	return errors.E(op, errNotImplemented)
}

// Methods that do not apply to this server.

func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
