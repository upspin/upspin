// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface.
package main // import "upspin.io/cmd/keyserver"

import (
	"flag"

	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/serverutil"
	"upspin.io/serverutil/keyserver"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/key/transports"

	// Possible storage backends.
	"upspin.io/cloud/https"
	_ "upspin.io/cloud/storage/disk"
)

var (
	testUser    = flag.String("test_user", "", "initialize a test `user` (localhost, inprocess only)")
	testSecrets = flag.String("test_secrets", "", "initialize test user with the secrets in this `directory`")
)

func main() {
	keyserver.Main(setupTestUser)
	https.ListenAndServeFromFlags(nil)
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
	if flags.InsecureHTTP {
		if !serverutil.IsLoopback(flags.HTTPAddr) {
			log.Fatal("cannot use -test_user flag on an insecure connection except on -http=localhost:port")
		}
	} else if !serverutil.IsLoopback(flags.HTTPSAddr) {
		log.Fatal("cannot use -test_user flag on a secure connection except on -https=localhost:port")
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
