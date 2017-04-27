// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path"

	"upspin.io/config"
	"upspin.io/dir/dircache"
	"upspin.io/flags"
)

func main() {
	flags.Parse(flags.Server, "cachedir")

	// Load configuration and keys for this server. It needn't have a real username.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		fmt.Printf("reading config: %s\n", err)
		os.Exit(1)
	}
	dircache.DumpLog(cfg, path.Join(flags.CacheDir, "dircache"))
}
