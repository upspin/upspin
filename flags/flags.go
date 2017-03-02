// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags defines command-line flags to make them consistent between binaries.
// Not all flags make sense for all binaries.
package flags // import "upspin.io/flags"

import (
	"flag"
	"fmt"
	"strings"

	"upspin.io/log"
)

// flagVar represents a flag in this package.
type flagVar struct {
	set  func()        // Set the value at parse time.
	arg  func() string // Return the argument to set the flag.
	arg2 func() string // Return the argument to set the second flag; usually nil.
}

const (
	defaultBlockSize  = 1024 * 1024 // Keep in sync with upspin.BlockSize.]
	defaultHTTPAddr   = ":80"
	defaultHTTPSAddr  = ":443"
	defaultLog        = "info"
	defaultServerKind = "inprocess"
)

var (
	// BlockSize is the block size used when writing large files. The default is 1MB.
	BlockSize = defaultBlockSize

	// CacheDir specifies the directory for the various file caches.
	CacheDir = defaultCacheDir

	// Config names the Upspin configuration file to use.
	Config = defaultConfig

	// HTTPAddr is the network address on which to listen for incoming
	// insecure network connections.
	HTTPAddr = defaultHTTPAddr

	// HTTPSAddr is the network address on which to listen for incoming
	// secure network connections.
	HTTPSAddr = defaultHTTPSAddr

	// LetsEncryptCache is the location of a file in which the Let's
	// Encrypt certificates are stored. The containing directory should
	// be owner-accessible only (chmod 0700).
	LetsEncryptCache = ""

	// Log sets the level of logging (implements flag.Value).
	Log logFlag

	// NetAddr is the publicly accessible network address of this server.
	NetAddr = ""

	// Project is the project name on GCP; used by servers, upspin-deploy,
	// and cmd/upspin setupdomain.
	Project = ""

	// ServerConfig specifies configuration options ("key=value") for servers.
	ServerConfig []string

	// ServerKind is the implementation kind of this server.
	ServerKind = defaultServerKind

	// StoreServerName is the Upspin user name of the StoreServer.
	StoreServerUser = ""

	// TLSCertFile and TLSKeyFile specify the location of a TLS
	// certificate/key pair used for serving TLS (HTTPS).
	TLSCertFile = ""
	TLSKeyFile  = ""
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]*flagVar{
	"addr": &flagVar{
		set: func() {
			stringVar(&NetAddr, "addr", "", "publicly accessible network address (`host:port`)")
		},
		arg: func() string { return strArg("addr", NetAddr, "") },
	},
	"blocksize": &flagVar{
		set: func() {
			intVar(&BlockSize, "blocksize", BlockSize, "`size` of blocks when writing large files")
		},
		arg: func() string {
			if BlockSize == defaultBlockSize {
				return ""
			}
			return fmt.Sprintf("-blocksize=%d", BlockSize)
		},
	},
	"cachedir": &flagVar{
		set: func() {
			stringVar(&CacheDir, "cachedir", CacheDir, "`directory` containing all file caches")
		},
		arg: func() string {
			return strArg("cachedir", CacheDir, defaultCacheDir)
		},
	},
	"config": &flagVar{
		set: func() {
			stringVar(&Config, "config", Config, "user's configuration `file`")
		},
		arg: func() string { return strArg("config", Config, defaultConfig) },
	},
	"http": &flagVar{
		set: func() {
			stringVar(&HTTPAddr, "http", HTTPAddr, "`address` for incoming insecure network connections")
		},
		arg: func() string { return strArg("http", HTTPAddr, defaultHTTPAddr) },
	},
	"https": &flagVar{
		set: func() {
			stringVar(&HTTPSAddr, "https", HTTPSAddr, "`address` for incoming secure network connections")
		},
		arg: func() string { return strArg("https", HTTPSAddr, defaultHTTPSAddr) },
	},
	"kind": &flagVar{
		set: func() {
			stringVar(&ServerKind, "kind", ServerKind, "server implementation `kind` (inprocess, gcp)")
		},
		arg: func() string { return strArg("kind", ServerKind, defaultServerKind) },
	},
	"letscache": &flagVar{
		set: func() {
			stringVar(&LetsEncryptCache, "letscache", "", "Let's Encrypt cache `directory`")
		},
		arg: func() string { return strArg("letscache", LetsEncryptCache, "") },
	},
	"log": &flagVar{
		set: func() {
			Log.Set("info")
			genVar(&Log, "log", "`level` of logging: debug, info, error, disabled")
		},
		arg: func() string { return strArg("log", Log.String(), defaultLog) },
	},
	"project": &flagVar{
		set: func() {
			stringVar(&Project, "project", Project, "GCP `project` name")
		},
		arg: func() string { return strArg("project", Project, "") },
	},
	"serverconfig": &flagVar{
		set: func() {
			genVar(configFlag{&ServerConfig}, "serverconfig", "comma-separated list of configuration options (key=value) for this server")
		},
		arg: func() string { return strArg("serverconfig", configFlag{&ServerConfig}.String(), "") },
	},
	"storeserveruser": &flagVar{
		set: func() {
			stringVar(&StoreServerUser, "storeserveruser", "", "user name of the StoreServer")
		},
		arg: func() string { return strArg("storeserveruser", StoreServerUser, "") },
	},
	"tls": &flagVar{
		set: func() {
			stringVar(&TLSCertFile, "tls_cert", "", "TLS Certificate `file` in PEM format")
			stringVar(&TLSKeyFile, "tls_key", "", "TLS Key `file` in PEM format")
		},
		arg:  func() string { return strArg("tls_cert", TLSCertFile, "") },
		arg2: func() string { return strArg("tls_key", TLSKeyFile, "") },
	},
}

var all = flag.NewFlagSet("globals", flag.ExitOnError)

func stringVar(variable *string, name, value, usage string) {
	flag.StringVar(variable, name, value, usage)
	all.StringVar(variable, name, value, usage)
}

func intVar(variable *int, name string, value int, usage string) {
	flag.IntVar(variable, name, value, usage)
	all.IntVar(variable, name, value, usage)
}

func genVar(variable flag.Value, name string, usage string) {
	flag.Var(variable, name, usage)
	all.Var(variable, name, usage)
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
	flag.Parse()
}

// Args returns a slice of -flag=value strings that will recreate
// the state of the flags. Flags set to their default value are elided.
func Args() []string {
	var args []string
	for _, flag := range flags {
		arg := flag.arg()
		if arg == "" {
			continue
		}
		args = append(args, arg)
		if flag.arg2 != nil {
			args = append(args, flag.arg2())
		}
	}
	return args
}

// Globals returns a flag set with the global variables declared within.
// The error handling is set to flag.ExitOnError, which works well within
// the upspin command's structure.
func Globals(name string) *flag.FlagSet {
	globals := flag.NewFlagSet(name, flag.ExitOnError)
	// The FlagSet type has no copy method, but we can manage.
	all.VisitAll(func(f *flag.Flag) {
		globals.Var(f.Value, f.Name, f.Usage)
	})
	return globals
}

// strArg returns a command-line argument that will recreate the flag,
// or the empty string if the value is the default.
func strArg(name, value, _default string) string {
	if value == _default {
		return ""
	}
	return "-" + name + "=" + value
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
