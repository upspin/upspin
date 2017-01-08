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
	"upspin.io/grpc/auth"
	"upspin.io/log"
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
	_, err := auth.NewClient(ctx, ce.NetAddr, auth.KeepAliveInterval, auth.NoSecurity, ce)
	if err == nil {
		return // cache server running
	}

	// Start a cache server.
	go func() {
		cmd := exec.Command("cacheserver", "-cache="+*cacheFlag, "-log="+log.Level())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}()

	// Wait for it.
	for tries := 0; tries < 3; tries++ {
		_, err := auth.NewClient(ctx, ce.NetAddr, auth.KeepAliveInterval, auth.NoSecurity, ce)
		if err == nil {
			fmt.Printf("Cacheserver also started\n")
			return // cache server running
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "Cacheserver may not have been started\n")
}
