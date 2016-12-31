// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storecache is a wrapper for a storage cache implementation that presents itself as a GRPC interface.
package main

import (
	"flag"
	"net"
	"os"

	"google.golang.org/grpc"

	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/grpc/dircacheserver"
	"upspin.io/grpc/storecacheserver"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/transports"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// This is required for context.InitContext to work.
	// TODO(adg): This seems wrong; fix it.
	_ "upspin.io/pack/plain"
)

const serverName = "cacheserver"

var (
	cacheFlag     = flag.String("cache", defaultCacheDir(), "`directory` for cache")
	cacheSizeFlag = flag.Int64("cachesize", 5e9, "max disk `bytes` for cache")
)

func main() {
	flags.Parse()

	// Load context and keys for this server. It needn't have a real username.
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Fatal(err)
	}

	// Serving address comes from config with flag overriding.
	var addr string
	if ce := ctx.CacheEndpoint(); ce.Transport == upspin.Remote {
		addr = string(ce.NetAddr)
	}
	if flags.NetAddr != "" {
		addr = flags.NetAddr
	}
	if len(addr) == 0 {
		log.Fatalf("no storage/dir cache network address specified")
	}

	// Stop the cache server recursing.
	ctx = context.SetCacheEndpoint(ctx, upspin.Endpoint{})

	// Calculate limits.
	maxRefBytes := (9 * (*cacheSizeFlag)) / 10
	maxLogBytes := maxRefBytes / 9

	ss, err := storecacheserver.New(ctx, *cacheFlag, maxRefBytes)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}

	ds, err := dircacheserver.New(ctx, *cacheFlag, maxLogBytes)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %s", err)
	}

	grpcServer := grpc.NewServer()
	proto.RegisterStoreServer(grpcServer, ss)
	proto.RegisterDirServer(grpcServer, ds)
	err = grpcServer.Serve(ln)
	log.Fatalf("serve: %v", err)
}

func defaultCacheDir() string {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	return homeDir + "/upspin"
}
