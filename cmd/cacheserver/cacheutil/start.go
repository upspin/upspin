// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cacheutil provides a mechanism to start the cacheserver
// if a config requires it and it is not already running.
// It is used by programs like upspin and upspinfs.
package cacheutil // import "upspin.io/cmd/cacheserver/cacheutil"

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"upspin.io/bind"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc"
	"upspin.io/upspin"
)

var (
	writethrough = flag.Bool("writethrough", false, "make storage cache writethrough")
	cacheSize    = flag.Int64("cachesize", 5e9, "max disk `bytes` for cache")
)

// detach detaches a process from the parent process group,
// on platforms that support it.
var detach = func(*exec.Cmd) {}

// Start starts the cacheserver if the config requires it and it is not already running.
func Start(cfg upspin.Config) {
	if cfg == nil {
		return
	}
	ce, err := rpc.CacheEndpoint(cfg)
	if err != nil || ce == nil {
		// TODO(adg): log error message?
		return // not using a cache server
	}

	// Ping the cache server.
	if err := ping(cfg, ce); err == nil {
		return // cache server running
	}

	// Start a cache server.
	cacheErrorChan := make(chan bool)
	go func() {
		cmd := exec.Command(
			"cacheserver",
			"-cachedir="+flags.CacheDir,
			"-log="+log.GetLevel(),
			fmt.Sprintf("-writethrough=%v", *writethrough),
			fmt.Sprintf("-cachesize=%d", *cacheSize),
			"-config="+flags.Config,
			"-addr="+flags.NetAddr)
		detach(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Info.Printf("Starting cacheserver: %s", err)
			fmt.Fprintf(os.Stderr, "Failed to start cacheserver; continuing without.\n")
			close(cacheErrorChan)
		}
	}()

	// Wait for it. Give up and continue without if it doesn't start in a timely fashion.
	for tries := 0; tries < 10; tries++ {
		time.Sleep(500 * time.Millisecond)
		select {
		case <-cacheErrorChan:
			return
		default:
		}
		if err := ping(cfg, ce); err == nil {
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Timed out waiting for cacheserver to start.\n")
}

// ping determines if the cacheserver is functioning.
func ping(cfg upspin.Config, ce *upspin.Endpoint) error {
	store, err := bind.StoreServer(cfg, *ce)
	if err != nil {
		return err
	}
	msg, _, _, err := store.Get(upspin.HealthMetadata)
	if err == nil {
		log.Debug.Printf("Cacheserver said %q", string(msg))
	}
	return err
}
