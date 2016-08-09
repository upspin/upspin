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

// We define the flags in two steps so clients don't have to write *flags.Flag.
// It also makes the documentation easier to read.

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

// Enable enables the listed flags.
func Enable(flags ...string) error {
	for _, f := range flags {
		switch f {
		case "config":
			flag.StringVar(&Config, f, Config, "comma-separated list of configuration options (key=value) for this server")
		case "context":
			flag.StringVar(&Context, f, Context, "context file")
		case "endpoint":
			flag.StringVar(&Endpoint, f, Endpoint, "endpoint of remote service for forwarding servers")
		case "https_addr":
			flag.StringVar(&HTTPSAddr, f, HTTPSAddr, "address for incoming network connections")
		case "log_file":
			flag.StringVar(&LogFile, f, LogFile, "name of the log file on GCP (empty to disable GCP logging)")
		case "log":
			Log.Set("info")
			flag.Var(&Log, f, "`level` of logging: debug, info, error, disabled")
		case "project":
			flag.StringVar(&Project, f, "", "The GCP project name, if any.")
		case "configfile":
			flag.StringVar(&ConfigFile, f, "", "Name of file with config parameters with one key=value per line")
		default:
			return fmt.Errorf("unknown flag %s", f)
		}
	}
	flag.Parse()
	return nil
}
