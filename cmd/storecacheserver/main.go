// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storecache is a wrapper for a storate cache implementation that presents it as a grpc interface.
package main

import (
	"net"
	"net/http"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/grpc/storecacheserver"
	"upspin.io/log"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
	_ "upspin.io/store/remote"

	// This is required for context.InitContext to work.
	// TODO(adg): This seems wrong; fix it.
	_ "upspin.io/pack/plain"
)

const serverName = "storecacheserver"

func main() {
	flags.Parse("addr", "config", "context", "https", "kind", "log")

	// Load context and keys for this server. It needn't have a real username.
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Fatal(err)
	}

	authConfig := auth.Config{Lookup: auth.PublicUserKeyService(ctx)}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s := storecacheserver.New(ctx, grpcSecureServer)
	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), s)

	// Serve from the root of the http namspace.
	http.Handle("/", grpcSecureServer.GRPCServer())
	ln, err := net.Listen("tcp", flags.NetAddr)
	if err != nil {
		log.Fatalf("listen: %s", err)
	}

	// Serve will not return if there is no error.
	err = http.Serve(ln, nil)
	if err != nil {
		log.Fatalf("serve: %s", err)
	}
}
