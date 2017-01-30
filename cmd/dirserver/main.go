// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux windows

// Dirserver is a wrapper for a directory implementation that presents it as an
// HTTP interface.
package main

import (
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/dir/filesystem"
	"upspin.io/dir/inprocess"
	"upspin.io/dir/server"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/rpc/dirserver"
	"upspin.io/serverutil/perm"
	"upspin.io/upspin"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"
	_ "upspin.io/pack/symm"

	// Load required transports
	_ "upspin.io/transports"
)

const serverName = "dirserver"

func main() {
	flags.Parse("addr", "config", "https", "kind", "storeservername", "letscache", "log", "project", "serverconfig", "tls")

	if flags.Project != "" {
		log.Connect(flags.Project, serverName)
		svr, err := metric.NewGCPSaver(flags.Project, "serverName", serverName)
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

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM) // only works on Linux, Windows.
	go watchTerm(c, dir)

	// Wrap with permission checks, if requested.
	var ready chan struct{}
	if flags.StoreServerUser != "" {
		ready := make(chan struct{})
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

func watchTerm(c chan os.Signal, dir upspin.DirServer) {
	<-c
	log.Printf("==== Got SIGTERM. Shutting down now.")
	dir.Close()
	// TODO: do more...?
	os.Exit(0) // Exit now or wait for the SIGKILL...?
}
