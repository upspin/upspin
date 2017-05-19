// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspinserver is a combined DirServer and StoreServer for use on
// stand-alone machines. It provides only the production implementations of the
// dir and store servers (dir/server and store/server).
package main // import "upspin.io/cmd/upspinserver"

import (
	"upspin.io/cloud/https"
	"upspin.io/serverutil/upspinserver"

	// Storage implementation.
	_ "upspin.io/cloud/storage/disk"
)

func main() {
	server, ready := upspinserver.Main()
	opt := https.OptionsFromFlags()
	opt.LetsEncryptHosts = []string{string(server.Addr)}
	https.ListenAndServe(ready, opt)
}
