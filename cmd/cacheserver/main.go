// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storecache is a wrapper for a storage cache implementation that presents itself as a GRPC interface.
package main

import (
	"net"

	"google.golang.org/grpc"

	"upspin.io/auth/grpcauth"
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

	authServer := grpcauth.NewServer(ctx, nil)

	ss, err := storecacheserver.New(ctx, authServer)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}

	ds, err := dircacheserver.New(ctx, authServer)
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
