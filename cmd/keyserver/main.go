// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface.
package main

import (
	"flag"
	"net"
	"net/http"

	"upspin.io/cloud/gcpmetric"
	"upspin.io/cloud/https"
	cloudLog "upspin.io/cloud/log"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/key/inprocess"
	"upspin.io/key/server"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/rpc/keyserver"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/key/transports"
)

const (
	// serverName is the upspin username for this server.
	serverName = "keyserver"

	// metricSampleSize is the size of the sample from which pick one metric
	// to save.
	metricSampleSize = 100

	// metricMaxQPS is the maximum number of metric batches to save per
	// second.
	metricMaxQPS = 5
)

var (
	testUser    = flag.String("test_user", "", "initialize a test `user` (localhost, inprocess only)")
	testSecrets = flag.String("test_secrets", "", "initialize test user with the secrets in this `directory`")
	// The format of the email config file must be lines: api key, incoming email provider user name and password.
	mailConfigFile = flag.String("mail_config", "", "config file name for incoming email signups")
)

func main() {
	flags.Parse(flags.Server, "kind", "project", "serverconfig")

	if flags.Project != "" {
		cloudLog.Connect(flags.Project, serverName)
		// Disable logging locally so we don't pay the price of local
		// unbuffered writes on a busy server.
		log.SetOutput(nil)
		svr, err := gcpmetric.NewSaver(flags.Project, metricSampleSize, metricMaxQPS, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Create a new key implementation.
	var key upspin.KeyServer
	switch flags.ServerKind {
	case "inprocess":
		key = inprocess.New()
	case "server":
		key, err = server.New(flags.ServerConfig...)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)

	}
	if err != nil {
		log.Fatalf("Setting up KeyServer: %v", err)
	}

	// Special hack for bootstrapping the inprocess key server.
	setupTestUser(key)

	httpStore := keyserver.New(cfg, key, upspin.NetAddr(flags.NetAddr))
	http.Handle("/api/Key/", httpStore)

	if logger, ok := key.(server.Logger); ok {
		http.Handle("/log", logHandler{logger: logger})
	}
	if *mailConfigFile != "" {
		f := cfg.Factotum()
		if f == nil {
			log.Fatal("supplied config must include keys when -mail_config set")
		}
		h, err := newSignupHandler(f, key, *mailConfigFile, flags.Project)
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/signup", h)
	} else {
		log.Println("-mail_config not set, /signup deactivated")
	}

	https.ListenAndServeFromFlags(nil, "keyserver")
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
