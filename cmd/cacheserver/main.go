// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/version"
)

const cmdName = "cacheserver"

func main() {
	flag.Usage = usage
	flags.Parse(flags.Server, "cachedir", "cachesize", "version")

	if flags.Version {
		fmt.Print(version.Version())
		return
	}

	// Load configuration and keys for this server. It needn't have a real username.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Set any flags contained in the config.
	if err := config.SetFlagValues(cfg, cmdName); err != nil {
		log.Fatalf("%s: %s", cmdName, err)
	}

	// Serving address comes from config with flag overriding.
	var addr string
	ce := cfg.CacheEndpoint()
	if !ce.Unassigned() {
		addr = string(ce.NetAddr)
	}
	if flags.NetAddr != "" {
		addr = flags.NetAddr
	}
	if len(addr) == 0 {
		log.Fatalf("no storage/dir cache network address specified")
	}

	// Start the server and wait until it terminates.
	done, err := serve(cfg, addr)
	if err != nil {
		log.Fatalf("cacheserver: %s", err)
	}
	if err := <-done; err != nil {
		log.Fatalf("cacheserver: %s", err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: cacheserver [flags]")
	fmt.Fprintln(os.Stderr, "For more information about cacheserver, run")
	fmt.Fprintln(os.Stderr, "\tgo doc upspin.io/cmd/cacheserver")
	fmt.Fprintln(os.Stderr, "")
	flag.PrintDefaults()
}
