// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A FUSE driver for Upspin.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/key/usercache"
	"upspin.io/log"

	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/transports"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flags.Parse("context", "log")

	if flag.NArg() != 1 {
		usage()
	}

	// Normal setup, get context from file and push user cache onto context.
	cf, err := os.Open(flags.Context)
	if err != nil {
		log.Debug.Fatal(err)
	}
	ctx, err := context.InitContext(cf)
	if err != nil {
		log.Fatal(err)
	}
	ctx = usercache.Global(ctx)

	// Mount the file system and start serving.
	mountpoint, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal("can't determine absolute path to mount point %s: %s", flag.Arg(0), err)
	}
	done := do(ctx, mountpoint)
	<-done
}
