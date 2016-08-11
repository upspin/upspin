// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Fileserver is a directory and store implementation that serves local files through an Upspin grpc interface.
package main

import (
	"flag"
	"net/http"
	"os"
	"strings"

	"upspin.io/access"
	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports. We only use the KeyServer interface itself; we are DirServer and StoreServer.
	_ "upspin.io/key/transports"
)

var root = flag.String("root", os.Getenv("HOME"), "root of directory to serve")

var defaultAccess *access.Access

func main() {
	flags.Parse("context", "https")

	if *root == "" {
		log.Fatal("no root directory specified")
	}
	if !strings.HasSuffix(*root, "/") {
		*root += "/"
	}

	// Load context and keys for this server. It needs a real upspin username and keys.
	ctxfd, err := os.Open(flags.Context)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	context, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}

	defaultAccess, err = access.New(upspin.PathName(context.UserName()) + "/Access")
	if err != nil {
		log.Fatal(err)
	}

	config := auth.Config{Lookup: auth.PublicUserKeyService(context)}

	endpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(flags.HTTPSAddr),
	}

	grpcSecureServer, err := grpcauth.NewSecureServer(config)
	if err != nil {
		log.Fatal(err)
	}
	grpcServer := grpcSecureServer.GRPCServer()
	http.Handle("/", grpcServer)

	storeServer := NewStoreServer(context, endpoint, grpcSecureServer)
	proto.RegisterStoreServer(grpcServer, storeServer)

	dirServer := NewDirServer(context, endpoint, grpcSecureServer)
	proto.RegisterDirServer(grpcServer, dirServer)

	https.ListenAndServe("fileserver", flags.HTTPSAddr, nil)
}
