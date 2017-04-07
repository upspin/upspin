// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storeserver is a wrapper for a store implementation that presents it as an
// HTTP interface.
package main // import "upspin.io/cmd/storeserver"

import (
	"upspin.io/cloud/https"
	"upspin.io/serverutil/storeserver"

	// Storage implementation.
	_ "upspin.io/cloud/storage/disk"
)

func main() {
	ready := storeserver.Main()
	https.ListenAndServeFromFlags(ready)
}
