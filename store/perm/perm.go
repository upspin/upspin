// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package perm implements mutation permission checking for StoreServer
// implementations.
package perm

// Features:
// - Resolves remote Group files if necessary.
// - Blocks mutations to Store until it has had a chance to prove that either
//   there is no Group file and hence writes are free for all, or until the
//   Group file has been fully loaded. This prevents a window of vulnerability
//   where all writes would be allowed until the initial load is completed.
//
// TODOs:
// - Cache references so we don't need to retrieve the contents every time.
// - Poll more frequently if there is no control Group set up, so the StoreServer
//   updates faster when creating a new one for the first time.
// - Poll more frequently if the DirServer is unreachable (speeds up boot time).

import (
	"sync"

	"upspin.io/errors"
	serverPerm "upspin.io/serverutil/perm"
	"upspin.io/upspin"
)

// Store performs permission checking for StoreServer implementations.
type Store struct {
	upspin.StoreServer

	serverCtx upspin.Context
	user      upspin.UserName
	perm      *serverPerm.Perm
}

// perm tracks the users allowed to write to the Store.
type perm struct {
	// firstRun ensures no mutations can go through until we have resolved
	// mutation permission checking for the first time.
	firstRun sync.WaitGroup

	mu      sync.Mutex // protects the map.
	writers map[upspin.UserName]bool
}

// WrapStore wraps the given StoreServer with a StoreServer that checks access
// permissions.
func WrapStore(ctx upspin.Context, store upspin.StoreServer) *Store {
	s := &Store{
		StoreServer: store,
		serverCtx:   ctx,
		user:        ctx.UserName(),
		perm:        serverPerm.New(ctx),
	}
	return s
}

// Put implements upspin.StoreServer.
func (s *Store) Put(data []byte) (*upspin.Refdata, error) {
	const op = "store/perm.Put"

	if !s.perm.IsWriter(s.user) {
		return nil, errors.E(op, s.user, errors.Permission, errors.Errorf("user not authorized"))
	}
	return s.StoreServer.Put(data)
}

// Delete implements upspin.StoreServer.
func (s *Store) Delete(ref upspin.Reference) error {
	const op = "store/perm.Delete"

	if !s.perm.IsWriter(s.user) {
		return errors.E(op, s.user, errors.Permission, errors.Errorf("user not authorized"))
	}
	return s.StoreServer.Delete(ref)
}

// Dial implements upspin.Service.
func (s *Store) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/perm.Dial"
	service, err := s.StoreServer.Dial(context, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newS := *s
	newS.user = context.UserName()
	newS.StoreServer = service.(upspin.StoreServer)
	return &newS, nil
}
