// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface.
package main // import "upspin.io/cmd/keyserver"

import (
	"flag"
	"net"

	"upspin.io/cloud/gcpmetric"
	cloudLog "upspin.io/cloud/log"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/serverutil/keyserver"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/key/transports"

	// Possible storage backends.
	_ "upspin.io/cloud/storage/disk"
	_ "upspin.io/cloud/storage/gcs"
)

const (
	// serverName is the name of this server.
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
)

func main() {
	flags.Register("project")

	if flags.Project != "" {
		cloudLog.Connect(flags.Project, serverName)
		// Disable logging locally so we don't pay the price of local
		// unbuffered writes on a busy server.
		log.SetOutput(nil)
		svr, err := gcpmetric.NewSaver(flags.Project, metricSampleSize, metricMaxQPS, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		}
		metric.RegisterSaver(svr)
	}

	keyserver.Main(setupTestUser)
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

// setupTestUser uses the -test_user and -test_secrets flags to bootstrap the
// inprocess key server with an initial user.
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
