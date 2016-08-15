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
	"strings"

	"upspin.io/log"
	"upspin.io/upspin"
)

var (
	// Config specifies configuration options ("key=value") for servers.
	Config []string

	// Context names the Upspin context file to use.
	Context = filepath.Join(os.Getenv("HOME"), "/upspin/rc")

	// NetAddr is the publicly accessible network address of this server.
	NetAddr upspin.NetAddr

	// HTTPSAddr is the network address on which to listen for incoming network connections.
	HTTPSAddr = "localhost:443"

	// Log sets the level of logging (implements flag.Value).
	Log logFlag

	// Project is the project name on GCP; used by servers only.
	Project = ""

	// ServerKind is the implementation kind of this server.
	ServerKind = "inprocess"
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]func(){
	"config": func() {
		flag.Var(configFlag{&Config}, "config", "comma-separated list of configuration options (key=value) for this server")
	},
	"context": func() {
		flag.StringVar(&Context, "context", Context, "context `file`")
	},
	"addr": func() {
		flag.Var(addrFlag{&NetAddr}, "addr", "publicly accessible network address (`host:port`)")
	},
	"https": func() {
		flag.StringVar(&HTTPSAddr, "https", HTTPSAddr, "`address` for incoming network connections")
	},
	"log": func() {
		Log.Set("info")
		flag.Var(&Log, "log", "`level` of logging: debug, info, error, disabled")
	},
	"project": func() {
		flag.StringVar(&Project, "project", Project, "GCP `project` name")
	},
	"kind": func() {
		flag.StringVar(&ServerKind, "kind", ServerKind, "server implementation `kind` (inprocess, gcp)")
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
func (f logFlag) String() string {
	return string(f)
}

// Set implements flag.Value.
func (f *logFlag) Set(level string) error {
	err := log.SetLevel(level)
	if err != nil {
		return err
	}
	*f = logFlag(log.Level())
	return nil
}

// Get implements flag.Getter.
func (logFlag) Get() interface{} {
	return log.Level()
}

type configFlag struct {
	s *[]string
}

// String implements flag.Value.
func (f configFlag) String() string {
	if f.s == nil {
		return ""
	}
	return strings.Join(*f.s, ",")
}

// Set implements flag.Value.
func (f configFlag) Set(s string) error {
	*f.s = strings.Split(strings.TrimSpace(s), ",")
	return nil
}

// Get implements flag.Getter.
func (f configFlag) Get() interface{} {
	if f.s == nil {
		return ""
	}
	return *f.s
}

type addrFlag struct {
	a *upspin.NetAddr
}

// String implements flag.Value.
func (f addrFlag) String() string {
	if f.a == nil {
		return ""
	}
	return string(*f.a)
}

// Set implements flag.Value.
func (f addrFlag) Set(s string) error {
	*f.a = upspin.NetAddr(s)
	return nil
}

// Get implements flag.Getter.
func (f addrFlag) Get() interface{} {
	if f.a == nil {
		return ""
	}
	return string(*f.a)
}
