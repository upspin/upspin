// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dirserver is a wrapper for a directory implementation that presents it as an
// HTTP interface.
package main // import "upspin.io/cmd/dirserver"

import (
	"upspin.io/cloud/https"
	"upspin.io/serverutil/dirserver"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/transports"

	_ "upspin.io/cloud/storage/disk"
)

func main() {
	ready := dirserver.Main()
	https.ListenAndServeFromFlags(ready)
}
