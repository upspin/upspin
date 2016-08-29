// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storecache is a wrapper for a storage cache implementation that presents itself as a grpc interface.
package main

import (
	"net"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/grpc/storecacheserver"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"

	// Load useful packers
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// This is required for context.InitContext to work.
	// TODO(adg): This seems wrong; fix it.
	_ "upspin.io/pack/plain"
)

const serverName = "storecacheserver"

func main() {
	flags.Parse()

	// Load context and keys for this server. It needn't have a real username.
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Fatal(err)
	}

	// Serving address comes from config with flag overriding.
	var addr string
	ce := ctx.StoreCacheEndpoint()
	if ce.Transport == upspin.Remote {
		addr = string(ce.NetAddr)
	}
	if flags.NetAddr != "" {
		addr = flags.NetAddr
	}

	// Stop the cache server recursing.
	ctx = context.SetStoreCacheEndpoint(ctx, upspin.Endpoint{})

	authConfig := auth.Config{Lookup: auth.PublicUserKeyService(ctx)}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s, err := storecacheserver.New(ctx, grpcSecureServer)
	if err != nil {
		log.Fatalf("opening cache: %s", err)
	}
	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), s)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %s", err)
	}
	grpcSecureServer.GRPCServer().Serve(ln)
}
