// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags defines command-line flags to make them consistent between binaries.
// Not all flags make sense for all binaries.
package flags // import "upspin.io/flags"

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/log"
)

var (
	// BlockSize is the block size used when writing large files. The default is 1MB.
	BlockSize = 1024 * 1024 // Keep in sync with upspin.BlockSize.

	// CacheDir specifies the directory for the various file caches.
	CacheDir = filepath.Join(os.Getenv("HOME"), "upspin")

	// Config names the Upspin configuration file to use.
	Config = filepath.Join(os.Getenv("HOME"), "upspin", "config")

	// HTTPAddr is the network address on which to listen for incoming
	// insecure network connections.
	HTTPAddr = ":80"

	// HTTPSAddr is the network address on which to listen for incoming
	// secure network connections.
	HTTPSAddr = ":443"

	// LetsEncryptCache is the location of a file in which the Let's
	// Encrypt certificates are stored. The containing directory should
	// be owner-accessible only (chmod 0700).
	LetsEncryptCache string

	// Log sets the level of logging (implements flag.Value).
	Log logFlag

	// NetAddr is the publicly accessible network address of this server.
	NetAddr string

	// Project is the project name on GCP; used by servers and
	// cmd/upspin setupdomain.
	Project = ""

	// ServerConfig specifies configuration options ("key=value") for servers.
	ServerConfig []string

	// ServerKind is the implementation kind of this server.
	ServerKind = "inprocess"

	// StoreServerName is the Upspin user name of the StoreServer.
	StoreServerUser = ""

	// TLSCertFile and TLSKeyFile specify the location of a TLS
	// certificate/key pair used for serving TLS (HTTPS).
	TLSCertFile string
	TLSKeyFile  string
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]func(){
	"addr": func() {
		flag.StringVar(&NetAddr, "addr", NetAddr, "publicly accessible network address (`host:port`)")
	},
	"blocksize": func() {
		flag.IntVar(&BlockSize, "blocksize", BlockSize, "`size` of blocks when writing large files")
	},
	"cachedir": func() {
		flag.StringVar(&CacheDir, "cachedir", CacheDir, "`directory` containing all file caches")
	},
	"config": func() {
		flag.StringVar(&Config, "config", Config, "user's configuration `file`")
	},
	"http": func() {
		flag.StringVar(&HTTPAddr, "http", HTTPAddr, "`address` for incoming insecure network connections")
	},
	"https": func() {
		flag.StringVar(&HTTPSAddr, "https", HTTPSAddr, "`address` for incoming secure network connections")
	},
	"kind": func() {
		flag.StringVar(&ServerKind, "kind", ServerKind, "server implementation `kind` (inprocess, gcp)")
	},
	"letscache": func() {
		flag.StringVar(&LetsEncryptCache, "letscache", "", "Let's Encrypt cache `directory`")
	},
	"log": func() {
		Log.Set("info")
		flag.Var(&Log, "log", "`level` of logging: debug, info, error, disabled")
	},
	"project": func() {
		flag.StringVar(&Project, "project", Project, "GCP `project` name")
	},
	"serverconfig": func() {
		flag.Var(configFlag{&ServerConfig}, "serverconfig", "comma-separated list of configuration options (key=value) for this server")
	},
	"storeservername": func() {
		flag.StringVar(&StoreServerUser, "storeserveruser", StoreServerUser, "user name of the StoreServer")
	},
	"tls": func() {
		flag.StringVar(&TLSCertFile, "tls_cert", TLSCertFile, "TLS Certificate `file` in PEM format")
		flag.StringVar(&TLSKeyFile, "tls_key", TLSKeyFile, "TLS Key `file` in PEM format")
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
	*f = logFlag(log.GetLevel())
	return nil
}

// Get implements flag.Getter.
func (logFlag) Get() interface{} {
	return log.GetLevel()
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
	ss := strings.Split(strings.TrimSpace(s), ",")
	// Drop empty elements.
	for i := 0; i < len(ss); i++ {
		if ss[i] == "" {
			ss = append(ss[:i], ss[i+1:]...)
		}
	}
	*f.s = ss
	return nil
}

// Get implements flag.Getter.
func (f configFlag) Get() interface{} {
	if f.s == nil {
		return ""
	}
	return *f.s
}
