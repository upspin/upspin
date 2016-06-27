// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Fileserver is a directory and store implementation that serves local files through an Upspin grpc interface.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/context"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
)

var (
	storePort   = flag.Int("port", 8080, "TCP port number for store service; directory is +1")
	ctxfile     = flag.String("context", filepath.Join(os.Getenv("HOME"), "/upspin/rc.dirserver"), "context file to use to configure server")
	selfSigned  = flag.Bool("selfsigned", false, "Start server with a self-signed TLS certificate")
	certFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to TLS certificate file")
	certKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to TLS certificate key file")
	config      = flag.String("config", "", "Comma-separated list of configuration options for this server")
	logFile     = flag.String("logfile", "dirserver", "Name of the log file on GCP or empty for no GCP logging")
)

const upspinProject = "google.com:upspin"

func main() {
	flag.Parse()

	if *logFile != "" {
		log.Connect(upspinProject, *logFile)
	}

	if *selfSigned {
		*certFile = filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/cert.pem")
		*certKeyFile = filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/key.pem")
	}

	svr, err := metric.NewGCPSaver(upspinProject)
	if err != nil {
		log.Error.Printf("Can't start a metric saver for GCP project upspin. No metrics will be saved")
	} else {
		metric.RegisterSaver(svr)
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

	// If there are configuration options, set them now.
	if *config != "" {
		// TODO
	}

	config := auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *selfSigned,
	}

	errChan := make(chan error)
	storeEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(fmt.Sprintf("localhost:%d", *storePort)), // TODO: Should be domain name.
	}
	dirEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(fmt.Sprintf("localhost:%d", *storePort+1)), // TODO: Should be domain name.
	}

	grpcSecureServer, err := grpcauth.NewSecureServer(config, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	storeServer := NewStoreServer(context, storeEndpoint, grpcSecureServer)
	go storeServer.Run(errChan)

	grpcSecureServer, err = grpcauth.NewSecureServer(config, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	dirServer := NewDirServer(context, storeEndpoint, dirEndpoint, grpcSecureServer)
	go dirServer.Run(errChan)

	log.Fatal(<-errChan)
}

func colonPort(e upspin.Endpoint) string {
	addr := string(e.NetAddr)
	colon := strings.Index(addr, ":")
	if colon < 0 {
		log.Fatal("bad network address: no colon in %q", addr)
	}
	return addr[colon:]
}
