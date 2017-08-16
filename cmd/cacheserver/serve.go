// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"expvar"
	"flag"
	"net/http"

	"upspin.io/config"
	"upspin.io/dir/dircache"
	"upspin.io/flags"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/local"
	"upspin.io/rpc/storeserver"
	"upspin.io/store/storecache"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/transports"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

var (
	cacheSizeFlag = flag.Int64("cachesize", 5e9, "max disk `bytes` for cache")
	writethrough  = flag.Bool("writethrough", false, "make storage cache writethrough")
)

func serve(cfg upspin.Config, addr string) (<-chan error, error) {
	// Stop the cache server recursing.
	cfg = config.SetValue(cfg, "cache", "no")

	// Calculate limits.
	maxRefBytes := (9 * (*cacheSizeFlag)) / 10
	maxLogBytes := maxRefBytes / 9

	sc, blockFlusher, err := storecache.New(cfg, flags.CacheDir, maxRefBytes, *writethrough)
	if err != nil {
		return nil, err
	}
	ss := storeserver.New(cfg, sc, "")

	dc, err := dircache.New(cfg, flags.CacheDir, maxLogBytes, blockFlusher)
	if err != nil {
		return nil, err
	}
	ds := dirserver.New(cfg, dc, "")

	ln, err := local.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	// Use our own ServerMux so that we can run in the same
	// process as a server using the default one.
	mux := &http.ServeMux{}
	httpServer := &http.Server{Handler: mux}

	mux.Handle("/api/Store/", ss)
	mux.Handle("/api/Dir/", ds)
	mux.Handle("/debug/vars", expvar.Handler())
	done := make(chan error)
	go func() {
		done <- httpServer.Serve(ln)
	}()
	return done, nil
}
