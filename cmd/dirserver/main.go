// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dirserver is a wrapper for a directory implementation that presents it as an
// HTTP interface.
package main // import "upspin.io/cmd/dirserver"

import (
	"net/http"

	"upspin.io/cloud/gcpsaver"
	"upspin.io/cloud/https"
	cloudLog "upspin.io/cloud/log"
	"upspin.io/config"
	"upspin.io/dir/inprocess"
	"upspin.io/dir/server"
	"upspin.io/errors"
	"upspin.io/exp/dir/filesystem"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/rpc/dirserver"
	"upspin.io/serverutil/perm"
	"upspin.io/upspin"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/transports"
)

const (
	serverName    = "dirserver"
	samplingRatio = 1    // report all metrics
	maxQPS        = 1000 // unlimited metric reports per second
)

func main() {
	flags.Parse("addr", "config", "https", "kind", "storeservername", "letscache", "log", "project", "serverconfig", "tls")

	if flags.Project != "" {
		cloudLog.Connect(flags.Project, serverName)
		svr, err := gcpsaver.New(flags.Project, samplingRatio, maxQPS, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	// Load configuration and keys for this server. It needs a real upspin username and keys.
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Create a new store implementation.
	var dir upspin.DirServer
	err = nil
	switch flags.ServerKind {
	case "inprocess":
		dir = inprocess.New(cfg)
	case "filesystem":
		dir, err = filesystem.New(cfg, flags.ServerConfig...)
	case "server":
		dir, err = server.New(cfg, flags.ServerConfig...)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)
	}
	if err != nil {
		log.Fatalf("Setting up DirServer: %v", err)
	}

	// Wrap with permission checks, if requested.
	var ready chan struct{}
	if flags.StoreServerUser != "" {
		ready = make(chan struct{})
		dir, err = perm.WrapDir(cfg, ready, upspin.UserName(flags.StoreServerUser), dir)
		if err != nil {
			log.Fatalf("Can't wrap DirServer monitoring %s: %s", flags.StoreServerUser, err)
		}
	} else {
		log.Printf("Warning: no Writers Group file protection -- all access permitted")
	}

	httpDir := dirserver.New(cfg, dir, upspin.NetAddr(flags.NetAddr))
	http.Handle("/api/Dir/", httpDir)

	https.ListenAndServeFromFlags(ready, serverName)
}
