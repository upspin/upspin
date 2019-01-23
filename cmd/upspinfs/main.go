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
	"upspin.io/rpc/local"
	"upspin.io/version"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	"upspin.io/transports"
)

const cmdName = "upspinfs"

var (
	mountpointFlag = flag.String("mountpoint", "", "`directory` on which to mount file system")
	allowOther     = flag.Bool("allow_other", false, "if set, allow other users to see the mount point; if using this option ensure that mount point access is strictly controlled")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [-mountpoint] <mount point>\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flags.Parse(flags.Server, "cachedir", "cachesize", "prudent", "version")

	if flags.Version {
		fmt.Print(version.Version())
		return
	}

	// Normal setup, get configuration from file and push user cache onto config.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Debug.Fatal(err)
	}

	// Set any flags contained in the config.
	if err := config.SetFlagValues(cfg, cmdName); err != nil {
		log.Fatalf("%s: %s", cmdName, err)
	}

	transports.Init(cfg)

	// Start the cacheserver if needed.
	if cacheutil.Start(cfg) {
		// Using a cacheserver, adjust cache size for upspinfs down.
		flags.CacheSize = flags.CacheSize / 10
	}

	// Mount the file system and start serving.
	if *mountpointFlag != "" {
		if flag.NArg() > 0 {
			log.Fatalf("mount point specified as both flag and argument\n")
		}
	} else {
		if flag.NArg() != 1 {
			usage()
			os.Exit(2)
		}
		*mountpointFlag = flag.Arg(0)
	}
	mountpoint, err := filepath.Abs(*mountpointFlag)
	if err != nil {
		log.Fatalf("can't determine absolute path to mount point %s: %s", *mountpointFlag, err)
	}
	done := do(cfg, mountpoint, filepath.Join(flags.CacheDir, string(cfg.UserName())),
		flags.CacheSize, *allowOther)

	// Serve expvar data.
	ln, err := local.Listen("tcp", config.LocalName(cfg, cmdName))
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		ErrorLog: log.NewStdLogger(log.Debug),
	}
	go func() {
		log.Fatal(srv.Serve(ln))
	}()

	// Wait for an unmount.
	<-done
	srv.Close()
}
