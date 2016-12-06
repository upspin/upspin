// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

type Dir struct {
	upspin.DirServer

	serverCtx upspin.Context
	user      upspin.UserName
	perm      *Perm
}

// WrapDir wraps the given DirServer with a DirServer that checks access
// permissions.
func WrapDir(ctx upspin.Context, targetUser upspin.UserName, dir upspin.DirServer) (*Dir, error) {
	const op = "serverutil/perm.WrapDir"
	p, err := New(ctx, targetUser)
	if err != nil {
		return nil, errors.E(op, err)
	}
	err = p.Update()
	if err != nil {
		return nil, errors.E(op, err)
	}
	go p.updateLoop()
	return &Dir{
		DirServer: dir,
		user:      ctx.UserName(),
		perm:      p,
	}, nil
}

// Put implements upspin.DirServer.
func (d *Dir) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "store/perm.Put"
	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if p.IsRoot() && !d.perm.IsWriter(d.user) {
		return nil, errors.E(op, d.user, errors.Permission, errors.Errorf("user not authorized"))
	}
	return d.DirServer.Put(entry)
}

// Dial implements upspin.Service.
func (d *Dir) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/perm.Dial"
	service, err := d.DirServer.Dial(context, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newDir := *d
	newDir.user = context.UserName()
	newDir.DirServer = service.(upspin.DirServer)
	return &newDir, nil
}
