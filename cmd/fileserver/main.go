// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Fileserver is a directory and store implementation that serves local files through an Upspin grpc interface.
package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports. We only use the User interface itself; we are Directory and Store.
	_ "upspin.io/user/transports"
)

var (
	httpsAddr = flag.String("https_addr", "localhost:8000", "HTTPS listen address")
	ctxfile   = flag.String("context", filepath.Join(os.Getenv("HOME"), "/upspin/rc.fileserver"), "context file to use to configure server")
	root      = flag.String("root", os.Getenv("HOME"), "root of directory to serve")
)

func main() {
	flag.Parse()

	if *root == "" {
		log.Fatal("no root directory specified")
	}
	if !strings.HasSuffix(*root, "/") {
		*root += "/"
	}

	// Load context and keys for this server. It needs a real upspin username and keys.
	ctxfd, err := os.Open(*ctxfile)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	context, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}

	config := auth.Config{Lookup: auth.PublicUserKeyService()}

	endpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(*httpsAddr),
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
	proto.RegisterDirectoryServer(grpcServer, dirServer)

	https.ListenAndServe("fileserver", *httpsAddr, nil)
}
