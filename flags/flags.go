// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags defines command-line flags to make them consistent between binaries.
// Not all flags make sense for all binaries.
package flags // import "upspin.io/flags"

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"upspin.io/config"
	"upspin.io/log"
)

// flagVar represents a flag in this package.
type flagVar struct {
	set func()          // Set the value at parse time.
	arg func() []string // Return the arguments to set the flags.
}

const (
	defaultBlockSize  = 1024 * 1024 // Keep in sync with upspin.BlockSize.]
	defaultHTTPAddr   = ":80"
	defaultHTTPSAddr  = ":443"
	defaultLog        = "info"
	defaultServerKind = "inprocess"
)

// The Parse and Register functions bind these variables to their respective
// command-line flags.
var (
	// BlockSize ("blocksize") is the block size used when writing large files.
	// The default is 1MB.
	BlockSize = defaultBlockSize

	// CacheDir ("cachedir") specifies the directory for the various file
	// caches.
	defaultCacheDir = filepath.Join(config.Home(), "upspin")
	CacheDir        = defaultCacheDir

	// Config ("config") names the Upspin configuration file to use.
	defaultConfig = filepath.Join(config.Home(), "upspin", "config")
	Config        = defaultConfig

	// Log ("log") sets the level of logging (implements flag.Value).
	Log logFlag

	// NetAddr ("addr") is the publicly accessible network address of this
	// server.
	NetAddr = ""

	// Project ("project") is the project name on GCP; used by servers,
	// upspin-deploy, and cmd/upspin setupdomain.
	Project = ""

	// ServerConfig ("serverconfig") specifies configuration options for
	// servers in "key=value" pairs.
	ServerConfig []string

	// ServerKind ("kind") is the implementation kind of this server.
	ServerKind = defaultServerKind

	// StoreServerName ("storeserveruser") is the Upspin user name of the
	// StoreServer.
	StoreServerUser = ""

	// Prudent ("prudent") sets an extra security mode in the client to
	// check for malicious or buggy servers, at possible cost in
	// performance or convenience. Specifically, one check is that the
	// writer listed in a directory entry is either the owner or a user
	// currently with write permission. This protects against a forged
	// directory entry at the cost of potentially blocking a legitimate
	// file written by a user who no longer has write permission.
	Prudent = false
)

// The Parse and Register functions bind these variables to their respective
// command-line flags when called with the "https" identifier.
var (
	// HTTPAddr is the network address on which to listen for
	// incoming insecure network connections.
	HTTPAddr = defaultHTTPAddr

	// HTTPSAddr is the network address on which to listen for
	// incoming secure network connections.
	HTTPSAddr = defaultHTTPSAddr

	// InsecureHTTP specifies whether to serve insecure HTTP
	// on HTTPAddr, instead of serving HTTPS (secured by TLS) on HTTPSAddr.
	InsecureHTTP = false

	// LetsEncryptCache is the location of a file in which
	// the Let's Encrypt certificates are stored. The containing directory
	// should be owner-accessible only (chmod 0700).
	LetsEncryptCache = ""

	// TLSCertFile and TLSKeyFile specify the location of a TLS
	// certificate/key pair used for serving TLS (HTTPS).
	TLSCertFile = ""
	TLSKeyFile  = ""
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]*flagVar{
	"addr": strVar(&NetAddr, "addr", NetAddr, "publicly accessible network address (`host:port`)"),
	"blocksize": &flagVar{
		set: func() {
			flag.IntVar(&BlockSize, "blocksize", BlockSize, "`size` of blocks when writing large files")
		},
		arg: func() []string {
			if BlockSize == defaultBlockSize {
				return nil
			}
			return []string{fmt.Sprintf("-blocksize=%d", BlockSize)}
		},
	},
	"cachedir": strVar(&CacheDir, "cachedir", CacheDir, "`directory` containing all file caches"),
	"config":   strVar(&Config, "config", Config, "user's configuration `file`"),
	"kind":     strVar(&ServerKind, "kind", ServerKind, "server implementation `kind` (inprocess, gcp)"),
	"log": &flagVar{
		set: func() {
			Log.Set("info")
			flag.Var(&Log, "log", "`level` of logging: debug, info, error, disabled")
		},
		arg: func() []string { return strArg(Log.String(), "log", defaultLog) },
	},
	"project": strVar(&Project, "project", Project, "GCP `project` name"),
	"serverconfig": &flagVar{
		set: func() {
			flag.Var(configFlag{&ServerConfig}, "serverconfig", "comma-separated list of configuration options (key=value) for this server")
		},
		arg: func() []string { return strArg(configFlag{&ServerConfig}.String(), "serverconfig", "") },
	},
	"storeserveruser": strVar(&StoreServerUser, "storeserveruser", "", "user name of the StoreServer"),
	"prudent": &flagVar{
		set: func() {
			flag.BoolVar(&Prudent, "prudent", false, "protect against malicious directory server")
		},
		arg: func() []string {
			if !Prudent {
				return nil
			}
			return []string{"-prudent"}
		},
	},

	"https": &flagVar{
		set: func() {
			flag.StringVar(&HTTPAddr, "http", HTTPAddr, "`address` for incoming insecure network connections")
			flag.StringVar(&HTTPSAddr, "https", HTTPSAddr, "`address` for incoming secure network connections")
			flag.BoolVar(&InsecureHTTP, "insecure", false, "whether to serve insecure HTTP instead of HTTPS")
			flag.StringVar(&LetsEncryptCache, "letscache", "", "Let's Encrypt cache `directory`")
			flag.StringVar(&TLSCertFile, "tls_cert", "", "TLS Certificate `file` in PEM format")
			flag.StringVar(&TLSKeyFile, "tls_key", "", "TLS Key `file` in PEM format")
		},
		arg: func() []string {
			var args []string
			str := func(value, name, _default string) {
				arg := strArg(value, name, _default)
				if len(arg) == 0 {
					return
				}
				args = append(args, arg...)
			}
			str(HTTPAddr, "http", HTTPAddr)
			str(HTTPSAddr, "https", HTTPSAddr)
			if InsecureHTTP {
				args = append(args, "-insecure")
			}
			str(LetsEncryptCache, "letscache", "")
			str(TLSCertFile, "tls_cert", "")
			str(TLSKeyFile, "tls_key", "")
			return args
		},
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
	Register(names...)
	flag.Parse()
}

// Register registers the command-line flags for the given flag names.
// Passing zero names install all flags.
// Passing an unknown name triggers a panic.
//
// For example:
// 	flags.Register("config", "endpoint") // Register Config and Endpoint.
// or
// 	flags.Register() // Register all flags.
func Register(names ...string) {
	if len(names) == 0 {
		// Register all flags if no names provided.
		for _, flag := range flags {
			flag.set()
		}
	} else {
		for _, n := range names {
			flag, ok := flags[n]
			if !ok {
				panic(fmt.Sprintf("unknown flag %q", n))
			}
			flag.set()
		}
	}
}

// Args returns a slice of -flag=value strings that will recreate
// the state of the flags. Flags set to their default value are elided.
func Args() []string {
	var args []string
	for _, flag := range flags {
		arg := flag.arg()
		if len(arg) == 0 {
			continue
		}
		args = append(args, arg...)
	}
	return args
}

// strVar returns a flagVar for the given string flag.
func strVar(value *string, name, _default, usage string) *flagVar {
	return &flagVar{
		set: func() {
			flag.StringVar(value, name, _default, usage)
		},
		arg: func() []string {
			return strArg(*value, name, _default)
		},
	}
}

// strArg returns a command-line argument that will recreate the flag,
// or the empty string if the value is the default.
func strArg(value, name, _default string) []string {
	if value == _default {
		return nil
	}
	return []string{"-" + name + "=" + value}
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
