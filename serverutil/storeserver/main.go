// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storeserver is a wrapper for a store implementation that presents it as an
// HTTP interface. It provides the common code used by all storeserver commands.
package storeserver // import "upspin.io/serverutil/storeserver"

import (
	"net/http"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/exp/store/filesystem"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"
	"upspin.io/store/inprocess"
	"upspin.io/store/server"
	"upspin.io/upspin"

	// Directory transports to fetch write permissions.
	_ "upspin.io/transports"

	// Packers for reading Access and Group files.
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"
)

const serverName = "storeserver"

func Main() {
	flags.Parse(flags.Server, "kind", "serverconfig")

	// Load configuration and keys for this server. It needs a real upspin username and keys.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Create a new store implementation.
	var store upspin.StoreServer
	err = nil
	switch flags.ServerKind {
	case "inprocess":
		store = inprocess.New()
	case "server":
		store, err = server.New(flags.ServerConfig...)
	case "filesystem":
		store, err = filesystem.New(cfg, flags.ServerConfig...)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)
	}
	if err != nil {
		log.Fatalf("Setting up StoreServer: %v", err)
	}

	// Wrap with permission checks.
	ready := make(chan struct{})
	store = perm.WrapStore(cfg, ready, store)

	httpStore := storeserver.New(cfg, store, upspin.NetAddr(flags.NetAddr))
	http.Handle("/api/Store/", httpStore)
	https.ListenAndServeFromFlags(ready, serverName)
}
