// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	"upspin.io/transports"
)

var (
	cacheFlag = flag.String("cache", defaultCacheDir(), "`directory` for file cache")
	wbFlag    = flag.Bool("writeback", true, "make storage cache writeback")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flags.Parse("config", "log")

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
	startCache(cfg)

	// Mount the file system and start serving.
	mountpoint, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatalf("can't determine absolute path to mount point %s: %s", flag.Arg(0), err)
	}
	done := do(cfg, mountpoint, *cacheFlag)
	<-done
}

func defaultCacheDir() string {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	return homeDir + "/upspin"
}

func startCache(cfg upspin.Config) {
	ce := cfg.CacheEndpoint()
	if ce.Transport == upspin.Unassigned {
		return // not using a cache server
	}

	// Ping the cache server.
	if err := ping(cfg, ce); err == nil {
		return // cache server running
	}

	// Start a cache server.
	cacheErrorChan := make(chan bool)
	wb := fmt.Sprintf("-writeback=%v", *wbFlag)
	go func() {
		cmd := exec.Command("cacheserver", "-cache="+*cacheFlag, "-log="+log.GetLevel(), wb)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Info.Printf("upspinfs: starting cacheserver: %s", err)
			fmt.Fprintf(os.Stderr, "Upspinfs failed to start cacheserver, continuing without.\n")
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

	fmt.Fprintf(os.Stderr, "Upspinfs timed out waiting for cacheserver to start.\n")
}

// ping determines if the cacheserver is functioning.
func ping(cfg upspin.Config, ce upspin.Endpoint) error {
	store, err := bind.StoreServer(cfg, ce)
	if err != nil {
		return err
	}
	msg, _, _, err := store.Get(upspin.HealthMetadata)
	if err == nil {
		log.Debug.Printf("upspinfs: cacheserver said %q", string(msg))
	}
	return err
}
