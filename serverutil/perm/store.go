// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// Store performs permission checking for StoreServer implementations.
type Store struct {
	upspin.StoreServer

	serverCtx upspin.Config
	user      upspin.UserName
	perm      *Perm
}

// WrapStore wraps the given StoreServer with a StoreServer that checks access
// permissions. It will only start polling the store permissions after the
// ready channel is closed.
func WrapStore(cfg upspin.Config, ready <-chan struct{}, store upspin.StoreServer) (*Store, error) {
	s := &Store{
		StoreServer: store,
		serverCtx:   cfg,
		user:        cfg.UserName(),
	}
	var err error
	s.perm, err = New(cfg, ready, cfg.UserName(), s.lookup, s.watch)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) IsWriter(user upspin.UserName) (bool) {
	return s.perm.IsWriter(user)
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
func (s *Store) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/perm.Dial"
	service, err := s.StoreServer.Dial(config, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newS := *s
	newS.user = config.UserName()
	newS.StoreServer = service.(upspin.StoreServer)
	return &newS, nil
}

func (s *Store) lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	dir, err := bind.DirServerFor(s.serverCtx, parsed.User())
	if err != nil {
		return nil, err
	}
	return dir.Lookup(name)
}

func (s *Store) watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	dir, err := bind.DirServerFor(s.serverCtx, parsed.User())
	if err != nil {
		return nil, err
	}
	return dir.Watch(name, order, done)
}
