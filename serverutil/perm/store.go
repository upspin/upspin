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
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// WrapStore wraps the given StoreServer with a StoreServer that checks access
// permissions. It will only start polling the store permissions after the
// ready channel is closed.
func WrapStore(cfg upspin.Config, ready <-chan struct{}, store upspin.StoreServer) upspin.StoreServer {
	const op errors.Op = "serverutil/perm.WrapStore"
	p := newPerm(op, cfg, ready, cfg.UserName(), nil, nil, noop, retry, nil)
	return p.WrapStore(store)
}

// WrapStore wraps the given StoreServer with a StoreServer that checks access
// permissions using Perm.
func (p *Perm) WrapStore(store upspin.StoreServer) upspin.StoreServer {
	return &storeWrapper{
		StoreServer: store,
		user:        p.cfg.UserName(),
		perm:        p,
	}
}

// storeWrapper performs permission checking for StoreServer implementations.
type storeWrapper struct {
	upspin.StoreServer

	user upspin.UserName // set by Dial
	perm *Perm
}

// Get implements upspin.StoreServer.
func (s *storeWrapper) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op errors.Op = "store/perm.Get"

	// Only storage administrators should be permitted to list references.
	if strings.HasPrefix(string(ref), string(upspin.ListRefsMetadata)) && s.user != s.perm.targetUser {
		return nil, nil, nil, errors.E(op, s.user, errors.Permission, "user not authorized")
	}
	return s.StoreServer.Get(ref)
}

// Put implements upspin.StoreServer.
func (s *storeWrapper) Put(data []byte) (*upspin.Refdata, error) {
	const op errors.Op = "store/perm.Put"

	if !s.perm.IsWriter(s.user) {
		return nil, errors.E(op, s.user, errors.Permission, "user not authorized")
	}
	return s.StoreServer.Put(data)
}

// Delete implements upspin.StoreServer.
func (s *storeWrapper) Delete(ref upspin.Reference) error {
	const op errors.Op = "store/perm.Delete"

	if s.user != s.perm.targetUser {
		return errors.E(op, s.user, errors.Permission, "user not authorized")
	}
	return s.StoreServer.Delete(ref)
}

// Dial implements upspin.Service.
func (s *storeWrapper) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "store/perm.Dial"
	service, err := s.StoreServer.Dial(cfg, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newS := *s
	newS.user = cfg.UserName()
	newS.StoreServer = service.(upspin.StoreServer)
	return &newS, nil
}
