// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

package main

import (
	_ "expvar"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"upspin.io/cmd/cacheserver/cacheutil"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	"upspin.io/transports"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flags.Parse(flags.Server, "cachedir")

	if flag.NArg() != 1 {
		usage()
	}

	// Normal setup, get configuration from file and push user cache onto config.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Debug.Fatal(err)
	}
	transports.Init(cfg)

	// Start the cache if needed.
	cacheutil.Start(cfg)

	// Mount the file system and start serving.
	mountpoint, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatalf("can't determine absolute path to mount point %s: %s", flag.Arg(0), err)
	}
	done := do(cfg, mountpoint, flags.CacheDir)

	// Serve expvar data on NetAddr.
	if len(flags.NetAddr) > 0 {
		go func() {
			log.Fatal(http.ListenAndServe(flags.NetAddr, nil))
		}()
	}
	<-done
}
