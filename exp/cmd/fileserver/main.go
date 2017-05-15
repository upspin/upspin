// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command fileserver serves a local file system as an Upspin tree.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/exp/filesystem"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/upspin"

	_ "upspin.io/key/transports"
	_ "upspin.io/pack/eeintegrity"
)

func main() {
	root := flag.String("root", "", "`path` to local file system root")

	flags.Parse(flags.Server)

	if *root == "" {
		fmt.Fprintln(os.Stderr, "fileserver: the -root flag must be specified")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	s, err := filesystem.New(cfg, *root)
	if err != nil {
		log.Fatal(err)
	}

	addr := upspin.NetAddr(flags.NetAddr)
	http.Handle("/api/Store/", storeserver.New(cfg, s.StoreServer(), addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, s.DirServer(), addr))

	https.ListenAndServeFromFlags(nil)
}
