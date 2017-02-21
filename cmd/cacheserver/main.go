// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Cacheserver implements a directory and storage cache for Upspin. It is a
// long-lived process that interposes itself between the client and the remote
// services, presenting itself as a local HTTP server that behaves just like the
// remote ones. In its default mode, it runs in writeback mode, which means the
// writes are asynchronous and appear to complete quickly, but may take longer to
// propagate to the servers. A flag sets writethrough mode instead, which operates
// synchronously and more slowly, but also more safely. Cacheserver uses local disk
// to store data it has read or written. The size of the local disk area is
// configurable with a flag.
//
// The "cache:" key should be set in the config file to enable the cacheserver. It
// will be started automatically if it is not already running, and continues to run
// once the program that started it has exited.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"upspin.io/config"
	"upspin.io/dir/dircache"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/store/storecache"

	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/transports"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

const serverName = "cacheserver"

var (
	cacheSizeFlag = flag.Int64("cachesize", 5e9, "max disk `bytes` for cache")
	writethrough  = flag.Bool("writethrough", false, "make storage cache writethrough")
)

func main() {
	flag.Usage = usage
	flags.Parse()

	// Load configuration and keys for this server. It needn't have a real username.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Serving address comes from config with flag overriding.
	var addr string
	if ce := cfg.CacheEndpoint(); ce.Transport == upspin.Remote {
		addr = string(ce.NetAddr)
	}
	if flags.NetAddr != "" {
		addr = flags.NetAddr
	}
	if len(addr) == 0 {
		log.Fatalf("no storage/dir cache network address specified")
	}

	// Stop the cache server recursing.
	cfg = config.SetCacheEndpoint(cfg, upspin.Endpoint{})

	// Calculate limits.
	maxRefBytes := (9 * (*cacheSizeFlag)) / 10
	maxLogBytes := maxRefBytes / 9

	sc, blockFlusher, err := storecache.New(cfg, flags.CacheDir, maxRefBytes, *writethrough)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}
	ss := storeserver.New(cfg, sc, "")

	dc, err := dircache.New(cfg, flags.CacheDir, maxLogBytes, blockFlusher)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}
	ds := dirserver.New(cfg, dc, "")

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %s", err)
	}

	http.Handle("/api/Store/", ss)
	http.Handle("/api/Dir/", ds)
	err = http.Serve(ln, nil)
	log.Fatalf("serve: %v", err)
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: cacheserver [flags]")
	fmt.Fprintln(os.Stderr, "For more information about cacheserver, run")
	fmt.Fprintln(os.Stderr, "\tgo doc upspin.io/cmd/cacheserver\n")
	flag.PrintDefaults()
	os.Exit(2)
}
