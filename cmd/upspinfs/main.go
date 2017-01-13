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

	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/transport/auth"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	"upspin.io/transports"
)

var (
	cacheFlag = flag.String("cache", defaultCacheDir(), "`directory` for file cache")
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
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Debug.Fatal(err)
	}
	transports.Init(ctx)

	// Start the cache if needed.
	startCache(ctx)

	// Mount the file system and start serving.
	mountpoint, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatalf("can't determine absolute path to mount point %s: %s", flag.Arg(0), err)
	}
	done := do(ctx, mountpoint, *cacheFlag)
	<-done
}

func defaultCacheDir() string {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	return homeDir + "/upspin"
}

func startCache(ctx upspin.Context) {
	ce := ctx.CacheEndpoint()
	if ce.Transport == upspin.Unassigned {
		return // not using a cache server
	}

	// Dial the cache server.
	ac, err := auth.NewClient(ctx, ce.NetAddr, auth.NoSecurity, ce)
	if err == nil {
		ac.Close()
		return // cache server running
	}

	// Start a cache server.
	cacheErrorChan := make(chan bool)
	go func() {
		cmd := exec.Command("cacheserver", "-cache="+*cacheFlag, "-log="+log.Level())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			log.Info.Printf("upspinfs: starting cacheserver: %s", err)
			fmt.Fprintf(os.Stderr, "Upspinfs failed to start cacheserver, continuing without.\n")
			close(cacheErrorChan)
		}
	}()

	// Wait for it. Give up and continue without if it doesn't start in a timely fashion.
	for tries := 0; tries < 3; tries++ {
		time.Sleep(500 * time.Millisecond)
		select {
		case <-cacheErrorChan:
			return
		default:
		}
		ac, err := auth.NewClient(ctx, ce.NetAddr, auth.NoSecurity, ce)
		if err == nil {
			fmt.Printf("Upspinfs started a cacheserver\n")
			ac.Close()
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Upspinfs timed out waiting for cacheserver to start.\n")
}
