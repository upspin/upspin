// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows
// +build !openbsd

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
	"upspin.io/rpc/local"
	"upspin.io/version"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	"upspin.io/transports"
)

const cmdName = "upspinfs"

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flags.Parse(flags.Server, "cachedir", "prudent", "version")

	if flags.Version {
		fmt.Print(version.Version())
		return
	}

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}

	// Normal setup, get configuration from file and push user cache onto config.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Debug.Fatal(err)
	}

	// Set any flags contained in the config.
	if err := config.SetFlagValues(cfg, cmdName); err != nil {
		log.Printf("%T", err)
		log.Fatalf("%s: %s", cmdName, err)
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

	// Serve expvar data.
	ln, err := local.Listen("tcp", local.LocalName(cfg, cmdName))
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{}
	go func() {
		log.Fatal(srv.Serve(ln))
	}()

	// Wait for an unmount.
	<-done
	srv.Close()
}
