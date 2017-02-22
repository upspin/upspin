// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package transports is a helper package that aggregates the store imports.
// It has no functionality itself; it is meant to be imported, using an "underscore"
// import, as a convenient way to link with all the transport implementations.
package transports // import "upspin.io/store/transports"

import (
	"upspin.io/bind"
	"upspin.io/store/inprocess"
	"upspin.io/upspin"

	_ "upspin.io/store/remote"
	_ "upspin.io/store/unassigned"
)

func init() {
	bind.RegisterStoreServer(upspin.InProcess, inprocess.New())
}
