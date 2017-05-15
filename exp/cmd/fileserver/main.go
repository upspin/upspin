// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command fileserver serves a local file system as an Upspin tree.
package main

import (
	"flag"
	"net/http"
	"os"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/upspin"

	filesystem_dir "upspin.io/exp/dir/filesystem"
	filesystem_store "upspin.io/exp/store/filesystem"

	_ "upspin.io/key/transports"
	_ "upspin.io/pack/eeintegrity"
)

func main() {
	root := flag.String("root", os.Getenv("PWD"), "`path` to local file system root")

	flags.Parse(flags.Server)

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	opt := "root=" + *root
	dir, err := filesystem_dir.New(cfg, opt)
	if err != nil {
		log.Fatal(err)
	}
	store, err := filesystem_store.New(cfg, opt)
	if err != nil {
		log.Fatal(err)
	}

	addr := upspin.NetAddr(flags.NetAddr)
	http.Handle("/api/Store/", storeserver.New(cfg, store, addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, dir, addr))

	https.ListenAndServeFromFlags(nil)
}
