// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filesystem

import (
	"time"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// StoreServer returns the StoreServer implementation for this Server.
func (s *Server) StoreServer() upspin.StoreServer {
	return storeServer{s}
}

type storeServer struct {
	*Server
}

func (s storeServer) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/filesystem.Dial"

	dialed := *s.Server
	dialed.user = cfg
	return storeServer{&dialed}, nil
}

var errNotDialed = errors.E(errors.Internal, errors.Str("must Dial before making request"))

func (s storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op = "store/filesystem.Get"

	if s.user == nil {
		return nil, nil, nil, errors.E(op, errNotDialed)
	}

	pathName := upspin.PathName(s.server.UserName()) + "/" + upspin.PathName(ref)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil, nil, errors.E(op, err)
	}

	// Verify that the requesting user can access this file.
	if ok, err := s.can(access.Read, parsed); err != nil {
		return nil, nil, nil, errors.E(op, err)
	} else if !ok {
		return nil, nil, nil, errors.E(op, parsed.Path(), access.ErrPermissionDenied)
	}

	data, err := s.readFile(pathName)
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

// Methods that are not implemented.

func (s storeServer) Put(ciphertext []byte) (*upspin.Refdata, error) {
	const op = "store/filesystem.Put"
	return nil, errors.E(op, errReadOnly)
}

func (s storeServer) Delete(ref upspin.Reference) error {
	const op = "store/filesystem.Delete"
	return errors.E(op, errReadOnly)
}
