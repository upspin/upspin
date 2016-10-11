// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as a grpc interface.
package main

import (
	"flag"
	"net"
	"net/http"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/grpc/keyserver"
	"upspin.io/key/gcp"
	"upspin.io/key/inprocess"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
)

// The upspin username for this server.
const serverName = "keyserver"

var (
	testUser    = flag.String("test_user", "", "initialize a test `user` (localhost, inprocess only)")
	testSecrets = flag.String("test_secrets", "", "initialize test user with the secrets in this `directory`")
	// The format of the email config file must be lines: api key, incoming email provider user name and password.
	emailConfigFile = flag.String("email_config", "", "config file name for incoming email signups")
)

func main() {
	flags.Parse("addr", "config", "context", "https", "kind", "log", "project", "tls")

	if flags.Project != "" {
		log.Connect(flags.Project, serverName)
		svr, err := metric.NewGCPSaver(flags.Project, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	// All we need in the context is some user name. It does not need to be registered as a "real" user.
	ctx := context.SetUserName(context.New(), serverName)

	// Create a new key implementation.
	var err error
	var key upspin.KeyServer
	switch flags.ServerKind {
	case "inprocess":
		key = inprocess.New()
	case "gcp":
		key, err = gcp.New(flags.Config...)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)

	}
	if err != nil {
		log.Fatalf("Setting up KeyServer: %v", err)
	}

	// Special hack for bootstrapping the inprocess key server.
	setupTestUser(key)

	authConfig := auth.Config{Lookup: func(userName upspin.UserName) (upspin.PublicKey, error) {
		user, err := key.Lookup(userName)
		if err != nil {
			return "", err
		}
		return user.PublicKey, nil
	}}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s := keyserver.New(ctx, key, grpcSecureServer, upspin.NetAddr(flags.NetAddr))
	proto.RegisterKeyServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	if *emailConfigFile != "" {
		mailHandler, err := newMailHandler(key, *emailConfigFile)
		if err != nil {
			log.Fatal(err)
		}
		http.HandleFunc("/mail", mailHandler.h)
	}

	// TODO(adg): this needs to be changed to keyserver. But it involves some metadata on GCP.
	const metadataKey = "userserver"

	https.ListenAndServe(metadataKey, flags.HTTPSAddr, &https.Options{
		CertFile: flags.TLSCertFile,
		KeyFile:  flags.TLSKeyFile,
	})
}

// isLocal returns true if the name only resolves to loopback addresses.
func isLocal(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func setupTestUser(key upspin.KeyServer) {
	if *testUser == "" {
		return
	}
	if *testSecrets == "" {
		log.Fatalf("cannot set up a test user without specifying -test_secrets")
	}

	// Sanity checks to make sure we're not doing this in production.
	if key.Endpoint().Transport != upspin.InProcess {
		log.Fatalf("cannot use testuser for endpoint %q", key.Endpoint())
	}
	if !isLocal(flags.HTTPSAddr) {
		log.Fatal("cannot use -testuser flag except on localhost:port")
	}

	f, err := factotum.NewFromDir(*testSecrets)
	if err != nil {
		log.Fatalf("unable to initialize factotum for %q: %v", *testUser, err)
	}
	userStruct := &upspin.User{
		Name:      upspin.UserName(*testUser),
		PublicKey: f.PublicKey(),
	}
	err = key.Put(userStruct)
	if err != nil {
		log.Fatalf("Put %q failed: %v", *testUser, err)
	}
}
