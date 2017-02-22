// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm // import "upspin.io/serverutil/perm"

import (
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// Dir wraps a DirServer and implements permission checking when creating new
// roots.
type Dir struct {
	upspin.DirServer

	user upspin.UserName
	perm *Perm
}

// WrapDir wraps the given DirServer with a DirServer that checks root-creation
// permissions. It will only start polling the store permissions after the
// ready channel is closed.
func WrapDir(cfg upspin.Config, ready <-chan struct{}, targetUser upspin.UserName, dir upspin.DirServer) (*Dir, error) {
	const op = "serverutil/perm.WrapDir"
	p, err := New(cfg, ready, targetUser, dir.Lookup, dir.Watch)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return &Dir{
		DirServer: dir,
		user:      cfg.UserName(),
		perm:      p,
	}, nil
}

// Put implements upspin.DirServer.
func (d *Dir) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "serverutil/perm.Put"
	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if p.IsRoot() && !d.perm.IsWriter(d.user) {
		return nil, errors.E(op, d.user, errors.Permission, errors.Str("user not authorized"))
	}
	return d.DirServer.Put(entry)
}

// Dial implements upspin.Service.
func (d *Dir) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op = "serverutil/perm.Dial"
	service, err := d.DirServer.Dial(config, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newDir := *d
	newDir.user = config.UserName()
	newDir.DirServer = service.(upspin.DirServer)
	return &newDir, nil
}
