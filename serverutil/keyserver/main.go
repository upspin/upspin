// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as an HTTP
// interface. It provides the common code used by all keyserver commands.
package keyserver // import "upspin.io/serverutil/keyserver"

import (
	"flag"
	"io/ioutil"
	"net/http"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
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

	yaml "gopkg.in/yaml.v2"
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

	http.Handle("/api/Key/", keyserver.New(cfg, key, upspin.NetAddr(flags.NetAddr)))

	if logger, ok := key.(server.Logger); ok {
		http.Handle("/log", logHandler{logger: logger})
	}

	signupURL := "https://" + flags.NetAddr + "/signup"
	f := cfg.Factotum()
	if f == nil {
		log.Fatal("keyserver: supplied config must include keys")
	}
	var mc *signup.MailConfig
	if *mailConfigFile == "" {
		log.Info.Printf("keyserver: WARNING: -mail_config not supplied; no emails will be sent, they will be logged instead")
		mc = &signup.MailConfig{Mail: mail.Logger(log.Info)}
	} else {
		data, err := ioutil.ReadFile(*mailConfigFile)
		if err != nil {
			log.Fatalf("keyserver: %v", err)
		}
		mc, err = parseMailConfig(data)
		if err != nil {
			log.Fatalf("keyserver: %v", err)
		}
	}
	http.Handle("/signup", signup.NewHandler(signupURL, f, key, mc))
}

// parseMailConfig reads YAML data and returns a signup.MailConfig
// 	apikey: SENDGRID_API_KEY
// 	notify: notify-signups@email.com
// 	from: sender@email.com
//	project: test
func parseMailConfig(data []byte) (*signup.MailConfig, error) {
	c := make(map[string]string)
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, errors.E(errors.IO, err)
	}
	for _, k := range []string{"apikey", "notify", "project", "from"} {
		if c[k] == "" {
			return nil, errors.E(errors.Invalid, errors.Errorf(`key "%s" is missing in config (need "apikey", "notify", "project" and "from")`, k))
		}
	}
	return &signup.MailConfig{
		Project: c["project"],
		Notify:  c["notify"],
		From:    c["from"],
		Mail:    sendgrid.New(c["apikey"]),
	}, nil
}
