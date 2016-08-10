// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags defines command-line flags to make them consistent between binaries.
// Not all flags make sense for all binaries.
package flags

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"upspin.io/log"
)

var (
	// Config is a comma-separated list of configuration options (key=value) for this server.
	Config = ""

	// Context names the Upspin context file to use.
	Context = filepath.Join(os.Getenv("HOME"), "/upspin/rc")

	// Endpoint specifies the endpoint of remote service (applies to forwarding servers only).
	Endpoint = "inprocess"

	// HTTPSAddr is the network address on which to listen for incoming network connections.
	HTTPSAddr = "localhost:443"

	// LogFile names the log file on GCP; leave empty to disable GCP logging.
	LogFile = ""

	// LogLevel sets the level of logging.
	Log logFlag

	// Project is the project name on GCP; used by servers only.
	Project = ""

	// ConfigFile is the name of a configuration file used by servers.
	ConfigFile = ""
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]func(){
	"config": func() {
		flag.StringVar(&Config, "config", Config, "comma-separated list of configuration options (key=value) for this server")
	},
	"context": func() {
		flag.StringVar(&Context, "context", Context, "context `file`")
	},
	"endpoint": func() {
		flag.StringVar(&Endpoint, "endpoint", Endpoint, "`endpoint` of remote service for forwarding servers")
	},
	"https_addr": func() {
		flag.StringVar(&HTTPSAddr, "https_addr", HTTPSAddr, "`address` for incoming network connections")
	},
	"log_file": func() {
		flag.StringVar(&LogFile, "log_file", LogFile, "name of the log `file` on GCP (empty to disable GCP logging)")
	},
	"project": func() {
		flag.StringVar(&Project, "project", Project, "GCP `project` name")
	},
	"config_file": func() {
		flag.StringVar(&ConfigFile, "config_file", ConfigFile, "`file` with config parameters, one key=value per line")
	},
	"log": func() {
		Log.Set("info")
		flag.Var(&Log, "log", "`level` of logging: debug, info, error, disabled")
	},
}

// Parse registers the command-line flags for the given flag names
// and calls flag.Parse. Passing zero names registers all flags.
// Passing an unknown name triggers a panic.
//
// For example:
// 	flags.Parse("config", "endpoint") // Register Config and Endpoint.
// or
// 	flags.Parse() // Register all flags.
func Parse(names ...string) {
	if len(names) == 0 {
		// Register all flags if no names provided.
		for _, fn := range flags {
			fn()
		}
	} else {
		for _, n := range names {
			fn, ok := flags[n]
			if !ok {
				panic(fmt.Sprintf("unknown flag %q", n))
			}
			fn()
		}
	}
	flag.Parse()
}

type logFlag string

// String implements flag.Value.
func (l *logFlag) String() string {
	return string(*l)
}

// Set implements flag.Value.
func (l *logFlag) Set(level string) error {
	err := log.SetLevel(level)
	if err != nil {
		return err
	}
	*l = logFlag(log.Level())
	return nil
}

// Get implements flag.Getter.
func (l *logFlag) Get() interface{} {
	return log.Level()
}
