// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package transports is a helper package that aggregates
// the key, store, and directory imports.
// It should be imported by client programs as a convenient
// way to link with all the transport implementations.
// It should not be imported by server programs.
package transports

import (
	"sync"

	"upspin.io/bind"
	"upspin.io/dir/inprocess"
	"upspin.io/upspin"

	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"

	_ "upspin.io/dir/remote"
	_ "upspin.io/dir/unassigned"
)

var bindOnce sync.Once

// Init initializes the transports for the given context.
// It is a no-op if passed a nil context or called more than once.
//
// It should be called only by client programs, directly after parsing a
// context. This handles the case where a context specifies an inprocess
// directory server and configures that server to talk to the specified store
// server.
func Init(ctx upspin.Context) {
	if ctx == nil {
		return
	}
	if ctx.DirEndpoint().Transport == upspin.InProcess {
		bindOnce.Do(func() {
			bind.RegisterDirServer(upspin.InProcess, inprocess.New(ctx))
		})
	}
}
