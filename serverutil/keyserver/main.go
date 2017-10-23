// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface. It provides the common code used by all keyserver commands.
package keyserver // import "upspin.io/serverutil/keyserver"

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"upspin.io/cloud/mail/sendgrid"
	"upspin.io/cloud/storage"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/key/inprocess"
	"upspin.io/key/server"
	"upspin.io/log"
	"upspin.io/rpc/keyserver"
	"upspin.io/serverutil/signup"
	"upspin.io/upspin"

	// Load required transports
	_ "upspin.io/key/transports"
)

// mailConfig specifies a config file name for the mail service.
// The format of the email config file must be lines: api key, incoming email
// provider user name and password.
var mailConfigFile = flag.String("mail_config", "", "config file name for mail service")

// Main starts the keyserver. If setup is not nil it is called with the
// instantiated KeyServer.
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
		var opts []storage.DialOpts
		for _, o := range flags.ServerConfig {
			opts = append(opts, storage.WithOptions(o))
		}
		var s storage.Storage
		s, err = storage.Dial("GCS", opts...)
		if err != nil {
			break
		}
		key = server.New(s)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)

	}
	if err != nil {
		log.Fatalf("Setting up KeyServer: %v", err)
	}

	if setup != nil {
		setup(key)
	}

	http.Handle("/api/Key/", keyserver.New(cfg, key, upspin.NetAddr(flags.NetAddr)))

	if logger, ok := key.(server.Logger); ok {
		http.Handle("/log", logHandler{logger: logger})
	}

	if *mailConfigFile != "" {
		signupURL := fmt.Sprintf("https://%s/signup", flags.NetAddr)
		f := cfg.Factotum()
		if f == nil {
			log.Fatal("keyserver: supplied config must include keys when -mail_config set")
		}
		project := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "project" {
				return
			}
			project = f.Value.String()
		})
		apiKey, err := parseMailConfig(*mailConfigFile)
		if err != nil {
			log.Fatalf("keyserver: %v", err)
		}
		m := sendgrid.New(apiKey)
		http.Handle("/signup", signup.NewHandler(signupURL, f, key, m, project))
	} else {
		log.Println("keyserver: -mail_config not set, /signup deactivated")
	}
}

func parseMailConfig(name string) (apiKey string, err error) {
	data, err := ioutil.ReadFile(name)
	if err != nil {
		return "", errors.E(errors.IO, err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	apiKey = strings.TrimSpace(lines[0])
	return
}
