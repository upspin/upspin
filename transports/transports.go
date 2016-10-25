// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package transports is a helper package that aggregates the key, store, and
// directory imports. It is meant to be imported as a convenient way to link
// with all the transport implementations.
// Programs that want to support inprocess directory servers should pass a
// context file to the Init function on startup.
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

// Init initializes the inprocess directory server if specified by the given
// context's directory endpoint. It otherwise does nothing and need not be
// called.
func Init(ctx upspin.Context) {
	if ctx.DirEndpoint().Transport == upspin.InProcess {
		bindOnce.Do(func() {
			bind.RegisterDirServer(upspin.InProcess, inprocess.New(ctx))
		})
	}
}
