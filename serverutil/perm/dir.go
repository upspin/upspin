// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm // import "upspin.io/serverutil/perm"

import (
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// WrapDir wraps the given DirServer with a DirServer that checks root-creation
// permissions. It will only start polling the store permissions after the
// ready channel is closed.
func WrapDir(cfg upspin.Config, ready <-chan struct{}, target upspin.UserName, dir upspin.DirServer) upspin.DirServer {
	const op errors.Op = "serverutil/perm.WrapDir"
	p := newPerm(op, cfg, ready, target, dir.Lookup, dir.Watch, noop, retry, nil)
	return p.WrapDir(dir)
}

// WrapDir wraps the given DirServer with a DirServer that checks root-creation
// permissions using Perm.
func (p *Perm) WrapDir(dir upspin.DirServer) upspin.DirServer {
	return &dirWrapper{
		DirServer: dir,
		user:      p.cfg.UserName(),
		perm:      p,
	}
}

// dirWrapper wraps a DirServer and implements permission checking when
// creating new roots.
type dirWrapper struct {
	upspin.DirServer

	user upspin.UserName
	perm *Perm
}

// Put implements upspin.DirServer.
func (d *dirWrapper) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op errors.Op = "serverutil/perm.Put"
	p, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if p.IsRoot() && !d.perm.IsWriter(d.user) {
		return nil, errors.E(op, d.user, errors.Permission, "user not authorized")
	}
	return d.DirServer.Put(entry)
}

// Dial implements upspin.Service.
func (d *dirWrapper) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op errors.Op = "serverutil/perm.Dial"
	service, err := d.DirServer.Dial(config, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	newDir := *d
	newDir.user = config.UserName()
	newDir.DirServer = service.(upspin.DirServer)
	return &newDir, nil
}
