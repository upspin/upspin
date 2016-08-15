// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filesystem

import (
	"strings"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

func New(ctx upspin.Context, options ...string) (upspin.StoreServer, error) {
	const op = "filserver.New"
	s := &server{server: ctx}

	// TODO(adg): put in common.go
	for _, opt := range options {
		switch {
		case strings.HasPrefix(opt, "root="):
			s.root = opt[len("root="):]
		default:
			return nil, errors.E(op, errors.Invalid, errors.Errorf("bad option %q", opt))
		}
	}

	// TODO(adg): check s.root exists and is readable.

	var err error
	s.defaultAccess, err = access.New(upspin.PathName(ctx.UserName()) + "/Access")
	if err != nil {
		return nil, errors.E(op, err)
	}

	return s, nil
}

type server struct {
	// Set by New.
	root          string
	server        upspin.Context
	defaultAccess *access.Access

	// Set by Dial.
	user upspin.Context
}

var errNotDialed = errors.E(errors.Internal, errors.Str("must Dial before making request"))

func (s *server) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	const op = "filesystem.Get"
	log.Debug.Println(op, ref)

	if s.user == nil {
		return nil, nil, errors.E(op, errNotDialed)
	}

	pathName := upspin.PathName(s.server.UserName()) + "/" + upspin.PathName(ref)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}

	// Verify that the requesting user can access this file.
	if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.Read, parsed); err != nil {
		return nil, nil, errors.E(op, err)
	} else if !ok {
		return nil, nil, errors.E(op, parsed.Path(), access.ErrPermissionDenied)
	}

	data, err := readFile(s.root, pathName)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}
	return data, nil, nil
}

func (s *server) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "filesystem.Dial"

	dialed := *s
	dialed.user = ctx.Copy()
	return &dialed, nil
}

func (s *server) Ping() bool {
	return true
}

func (s *server) Close() {
	// Nothing to do.
}

// Methods that are not implemented.

var errNotImplemented = errors.Str("not implemented")

func (s *server) Put(ciphertext []byte) (upspin.Reference, error) {
	return "", errNotImplemented
}

func (s *server) Delete(ref upspin.Reference) error {
	return errNotImplemented
}

// Methods that do not apply to this server.

func (s *server) Configure(options ...string) error {
	return errors.Str("store/filesystem: Configure should not be called")
}

func (s *server) Authenticate(upspin.Context) error {
	return errors.Str("filesystem/gcp: Authenticate should not be called")
}

func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
