// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"expvar"
	"flag"
	"net/http"
	"os"
	"path/filepath"

	"upspin.io/config"
	"upspin.io/dir/dircache"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/local"
	"upspin.io/rpc/storeserver"
	"upspin.io/store/storecache"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/transports"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"
)

var (
	writethrough = flag.Bool("writethrough", false, "make storage cache writethrough")
)

func serve(cfg upspin.Config, addr string) (<-chan error, error) {
	cachedCfg := cfg
	uncachedCfg := config.SetCacheEndpoint(cfg, upspin.Endpoint{})

	// Calculate limits.
	maxRefBytes := (9 * (flags.CacheSize)) / 10
	maxLogBytes := maxRefBytes / 9

	myCacheDir := filepath.Join(flags.CacheDir, string(cfg.UserName()))

	// Link old structure cache files into the new structure.
	relocate(flags.CacheDir, myCacheDir)

	sc, blockFlusher, err := storecache.New(uncachedCfg, myCacheDir, maxRefBytes, *writethrough)
	if err != nil {
		return nil, err
	}
	ss := storeserver.New(uncachedCfg, sc, "")

	dc, err := dircache.New(uncachedCfg, cachedCfg, myCacheDir, maxLogBytes, blockFlusher)
	if err != nil {
		return nil, err
	}
	ds := dirserver.New(uncachedCfg, dc, "")

	ln, err := local.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	// Use our own ServerMux so that we can run in the same
	// process as a server using the default one.
	mux := &http.ServeMux{}
	httpServer := &http.Server{
		Handler:  mux,
		ErrorLog: log.NewStdLogger(log.Debug),
	}

	mux.Handle("/api/Store/", ss)
	mux.Handle("/api/Dir/", ds)
	mux.Handle("/debug/vars", expvar.Handler())
	done := make(chan error)
	go func() {
		done <- httpServer.Serve(ln)
	}()
	return done, nil
}

// relocate links the old directory contents one level down into a
// user specific directory. By linking the files one at a time rather
// than linking or renaming the directories, we cause the least interference
// between old and new worlds should an old server still be running.
//
// TODO(p): when everyone has had a chance to convert, replace this with
// a routine that removes the old structure.
func relocate(old, new string) {
	if _, err := os.Stat(new); err == nil || !os.IsNotExist(err) {
		// Already done, do nothing.
		return
	}
	if err := os.MkdirAll(new, 0700); err != nil {
		log.Debug.Printf("cacheserver/relocate: %s", err)
		return
	}
	walkAndMove(old, new, "storewritebackqueue", nil)
	walkAndMove(old, new, "storecache", nil)
	walkAndMove(old, new, "dircache", nil)
}

// walkAndMove links old files into new structure.
func walkAndMove(oldDir, newDir, name string, info os.FileInfo) {
	old := filepath.Join(oldDir, name)
	new := filepath.Join(newDir, name)
	if info == nil {
		var err error
		info, err = os.Stat(old)
		if err != nil {
			log.Debug.Printf("cacheserver/walkAndMove: %s", err)
			return
		}
	}

	// Link files into new directory structure.
	if !info.Mode().IsDir() {
		if err := os.Link(old, new); err != nil {
			log.Debug.Printf("cacheserver/walkAndMove: %s", err)
		}
		return
	}
	if err := os.MkdirAll(new, 0700); err != nil {
		log.Debug.Printf("cacheserver/walkAndMove: %s", err)
		return
	}

	// Read and descend directories.
	f, err := os.Open(old)
	if err != nil {
		log.Debug.Printf("cacheserver/walkAndMove: %s", err)
		return
	}
	infos, err := f.Readdir(0)
	f.Close()
	if err != nil {
		log.Debug.Printf("cacheserver/walkAndMove: %s", err)
		return

	}
	for _, i := range infos {
		walkAndMove(old, new, i.Name(), i)
	}
}
