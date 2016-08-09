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
	"reflect"

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

// Parse sets up the command-line flags for the given flag variables
// and calls flag.Parse. Passing an unknown variable triggers a panic.
//
// For example:
// 	flags.Enable(&flags.Config, &flags.Endpoint)
func Parse(vars ...interface{}) error {
	// TODO(adg): make zero arguments register all flags.
	for i, v := range vars {
		unknown := false
		switch v := v.(type) {
		case *string:
			switch v {
			case &Config:
				flag.StringVar(v, "config", Config, "comma-separated list of configuration options (key=value) for this server")
			case &Context:
				flag.StringVar(v, "context", Context, "context file")
			case &Endpoint:
				flag.StringVar(v, "endpoint", Endpoint, "endpoint of remote service for forwarding servers")
			case &HTTPSAddr:
				flag.StringVar(v, "https_addr", HTTPSAddr, "address for incoming network connections")
			case &LogFile:
				flag.StringVar(v, "log_file", LogFile, "name of the log file on GCP (empty to disable GCP logging)")
			case &Project:
				flag.StringVar(v, "project", "", "GCP `project` name")
			case &ConfigFile:
				flag.StringVar(v, "configfile", "", "`file` with config parameters, one key=value per line")
			default:
				unknown = true
			}
		case *logFlag:
			switch v {
			case &Log:
				v.Set("info")
				flag.Var(v, "log", "`level` of logging: debug, info, error, disabled")
			default:
				unknown = true
			}
		default:
			unknown = true
		}
		if unknown {
			msg := fmt.Sprintf("flags: unknown flag (%#v, arg %d)", v, i)
			if reflect.TypeOf(v).Kind() != reflect.Ptr {
				msg += ", expected pointer type"
			}
			panic(msg)
		}
	}
	flag.Parse()
	return nil
}
