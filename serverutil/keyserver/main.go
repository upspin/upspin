// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface. It provides the common code used by all keyserver commands.
package keyserver // import "upspin.io/serverutil/keyserver"

import (
	"flag"
	"net/http"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/key/inprocess"
	"upspin.io/key/server"
	"upspin.io/log"
	"upspin.io/rpc/keyserver"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/key/transports"
)

// serverName is the name of this server.
const serverName = "keyserver"

// mailConfig specifies a config file name for the mail service.
// The format of the email config file must be lines: api key, incoming email
// provider user name and password.
var mailConfigFile = flag.String("mail_config", "", "config file name for mail service")

// Main starts the keyserver. If setup is not nil it is called with the
// instantiated KeyServer before it starts serving clients.
func Main(setup func(upspin.KeyServer)) {
	flags.Parse(flags.Server, "kind", "serverconfig")

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

	if setup != nil {
		setup(key)
	}

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
		log.Println("keyserver: -mail_config not set, /signup deactivated")
	}

	https.ListenAndServeFromFlags(nil, "keyserver")
}
