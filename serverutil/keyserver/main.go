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

	signupURL := "https://" + flags.NetAddr + "/signup"
	f := cfg.Factotum()
	if f == nil {
		log.Fatal("keyserver: supplied config must include keys")
	}
	mc := new(signup.MailConfig)
	if *mailConfigFile == "" {
		log.Info.Printf("keyserver: WARNING: -mail_config not supplied; no emails will be sent, they will be logged instead")
		mc.Mail = mail.Logger(log.Info)
	} else {
		data, err := ioutil.ReadFile(*mailConfigFile)
		if err != nil {
			log.Fatal("keyserver: %v", err)
		}
		mc, err = parseMailConfig(data)
		if err != nil {
			log.Fatal("keyserver: %v", err)
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
	var c struct{ APIKey, Project, Notify, From string }
	var m mail.Mail
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, errors.E(errors.IO, err)
	}
	if c.Project == "" {
		log.Info.Printf("keyserver: project name not supplied in mail config")
	}
	if c.APIKey == "" {
		log.Info.Printf("keyserver: WARNING: apikey is missing in config; no emails will be sent, they will be logged instead")
		m = mail.Logger(log.Info)
	} else {
		if c.Notify != "" && c.From == "" {
			log.Info.Printf("keyserver: missing from address in config, notifications will not be sent")
			c.Notify = ""
		}
		m = sendgrid.New(c.APIKey)
	}
	return &signup.MailConfig{
		Project: c.Project,
		Notify:  c.Notify,
		From:    c.From,
		Mail:    m,
	}, nil
}
