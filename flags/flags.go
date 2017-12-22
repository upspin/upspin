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
	"strconv"
	"strings"

	"upspin.io/config"
	"upspin.io/log"
	"upspin.io/upspin"
)

// flagVar represents a flag in this package.
type flagVar struct {
	set  func(fs *flag.FlagSet) // Set the value at parse time.
	arg  func() string          // Return the argument to set the flag.
	arg2 func() string          // Return the argument to set the second flag; usually nil.
}

const (
	defaultBlockSize  = upspin.BlockSize
	maxBlockSize      = upspin.MaxBlockSize
	defaultHTTPAddr   = ":80"
	defaultHTTPSAddr  = ":443"
	defaultLog        = "info"
	defaultServerKind = "inprocess"
	defaultCacheSize  = int64(5e9)
)

var (
	defaultCacheDir         = upspinDir("")
	defaultLetsEncryptCache = upspinDir("letsencrypt")
	defaultConfig           = upspinDir("config")
)

func upspinDir(subdir string) string {
	home, err := config.Homedir()
	if err != nil {
		log.Error.Printf("flags: could not locate home directory: %v", err)
		home = "."
	}
	return filepath.Join(home, "upspin", subdir)
}

// None is the set of no flags. It is rarely needed as most programs
// use either the Server or Client set.
var None = []string{}

// Server is the set of flags most useful in servers. It can be passed as the
// argument to Parse to set up the package for a server.
var Server = []string{
	"config", "log", "http", "https", "letscache", "tls", "addr", "insecure",
}

// Client is the set of flags most useful in clients. It can be passed as the
// argument to Parse to set up the package for a client.
var Client = []string{
	"config", "log", "blocksize", "prudent",
}

// The Parse and Register functions bind these variables to their respective
// command-line flags.
var (
	// BlockSize ("blocksize") is the block size used when writing large files.
	// The default is 1MB; it can be no larger than 1GB.
	BlockSize = defaultBlockSize

	// CacheDir ("cachedir") specifies the directory for the various file
	// caches.
	CacheDir = defaultCacheDir

	// CacheSize ("cachesize") specifies the maximum bytes used by
	// the various file caches. This is only approximate.
	CacheSize = defaultCacheSize

	// Config ("config") names the Upspin configuration file to use.
	Config = defaultConfig

	// HTTPAddr ("http") is the network address on which to listen for
	// incoming insecure network connections.
	HTTPAddr = defaultHTTPAddr

	// HTTPSAddr ("https") is the network address on which to listen for
	// incoming secure network connections.
	HTTPSAddr = defaultHTTPSAddr

	// InsecureHTTP ("insecure") specifies whether to serve insecure HTTP
	// on HTTPAddr, instead of serving HTTPS (secured by TLS) on HTTPSAddr.
	InsecureHTTP = false

	// LetsEncryptCache ("letscache") is the location of a file in which
	// the Let's Encrypt certificates are stored. The containing directory
	// should be owner-accessible only (chmod 0700).
	LetsEncryptCache = defaultLetsEncryptCache

	// Log ("log") sets the level of logging (implements flag.Value).
	Log logFlag

	// NetAddr ("addr") is the publicly accessible network address of this
	// server.
	NetAddr = ""

	// ServerConfig ("serverconfig") specifies configuration options for
	// servers in "key=value" pairs.
	ServerConfig []string

	// ServerKind ("kind") is the implementation kind of this server.
	ServerKind = defaultServerKind

	// Prudent ("prudent") sets an extra security mode in the client to
	// check for malicious or buggy servers, at possible cost in
	// performance or convenience. Specifically, one check is that the
	// writer listed in a directory entry is either the owner or a user
	// currently with write permission. This protects against a forged
	// directory entry at the cost of potentially blocking a legitimate
	// file written by a user who no longer has write permission.
	Prudent = false

	// TLSCertFile and TLSKeyFile ("tls") specify the location of a TLS
	// certificate/key pair used for serving TLS (HTTPS).
	TLSCertFile = ""
	TLSKeyFile  = ""

	// Version causes the program to print its release version and exit.
	// The printed version is only meaningful in released binaries.
	Version = false
)

// flags is a map of flag registration functions keyed by flag name,
// used by Parse to register specific (or all) flags.
var flags = map[string]*flagVar{
	"addr": strVar(&NetAddr, "addr", NetAddr, "publicly accessible network address (`host:port`)"),
	"blocksize": &flagVar{
		set: func(fs *flag.FlagSet) {
			usage := fmt.Sprintf("`size` of blocks when writing large files (default %d)", defaultBlockSize)
			fs.Var(&blockSize, "blocksize", usage)
		},
		arg: func() string {
			if BlockSize == defaultBlockSize {
				return ""
			}
			return fmt.Sprintf("-blocksize=%d", BlockSize)
		},
	},
	"cachedir": strVar(&CacheDir, "cachedir", CacheDir, "`directory` containing all file caches"),
	"cachesize": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.Int64Var(&CacheSize, "cachesize", defaultCacheSize, "maximum bytes for file caches")
		},
		arg: func() string {
			if CacheSize == defaultCacheSize {
				return ""
			}
			return fmt.Sprintf("-cachesize=%d", CacheSize)
		},
	},
	"config": strVar(&Config, "config", Config, "user's configuration `file`"),
	"http":   strVar(&HTTPAddr, "http", HTTPAddr, "`address` for incoming insecure network connections"),
	"https":  strVar(&HTTPSAddr, "https", HTTPSAddr, "`address` for incoming secure network connections"),
	"insecure": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.BoolVar(&InsecureHTTP, "insecure", false, "whether to serve insecure HTTP instead of HTTPS")
		},
		arg: func() string {
			if InsecureHTTP {
				return "-insecure"
			}
			return ""
		},
	},
	"kind":      strVar(&ServerKind, "kind", ServerKind, "server implementation `kind` (inprocess, server)"),
	"letscache": strVar(&LetsEncryptCache, "letscache", defaultLetsEncryptCache, "Let's Encrypt cache `directory`"),
	"log": &flagVar{
		set: func(fs *flag.FlagSet) {
			Log.Set("info")
			fs.Var(&Log, "log", "`level` of logging: debug, info, error, disabled")
		},
		arg: func() string { return strArg("log", Log.String(), defaultLog) },
	},
	"serverconfig": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.Var(configFlag{&ServerConfig}, "serverconfig", "comma-separated list of configuration options (key=value) for this server")
		},
		arg: func() string { return strArg("serverconfig", configFlag{&ServerConfig}.String(), "") },
	},
	"prudent": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.BoolVar(&Prudent, "prudent", false, "protect against malicious directory server")
		},
		arg: func() string {
			if !Prudent {
				return ""
			}
			return "-prudent"
		},
	},
	"tls": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.StringVar(&TLSCertFile, "tls_cert", "", "TLS Certificate `file` in PEM format")
			fs.StringVar(&TLSKeyFile, "tls_key", "", "TLS Key `file` in PEM format")
		},
		arg:  func() string { return strArg("tls_cert", TLSCertFile, "") },
		arg2: func() string { return strArg("tls_key", TLSKeyFile, "") },
	},
	"version": &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.BoolVar(&Version, "version", false, "print build version and exit")
		},
		arg: func() string {
			if !Version {
				return ""
			}
			return "-version"
		},
	},
}

// Parse registers the command-line flags for the given default flags list, plus
// any extra flag names, and calls flag.Parse. Passing no flag names in either
// list registers all flags. Passing an unknown name triggers a panic.
// The Server and Client variables contain useful default sets.
//
// Examples:
// 	flags.Parse(flags.Client) // Register all client flags.
//	flags.Parse(flags.Server, "cachedir") // Register all server flags plus cachedir.
// 	flags.Parse(nil) // Register all flags.
// 	flags.Parse(flags.None, "config", "endpoint") // Register only config and endpoint.
func Parse(defaultList []string, extras ...string) {
	ParseArgsInto(flag.CommandLine, os.Args[1:], defaultList, extras...)
}

// ParseInto is the same as Parse but accepts a FlagSet argument instead of
// using the default flag.CommandLine FlagSet.
func ParseInto(fs *flag.FlagSet, defaultList []string, extras ...string) {
	ParseArgsInto(fs, os.Args[1:], defaultList, extras...)
}

// ParseArgs is the same as Parse but uses the provided argument list
// instead of those provided on the command line. For ParseArgs, the
// initial command name should not be provided.
func ParseArgs(args, defaultList []string, extras ...string) {
	ParseArgsInto(flag.CommandLine, args, defaultList, extras...)
}

// ParseArgsInto is the same as ParseArgs but accepts a FlagSet argument instead of
// using the default flag.CommandLine FlagSet.
func ParseArgsInto(fs *flag.FlagSet, args, defaultList []string, extras ...string) {
	if len(defaultList) == 0 && len(extras) == 0 {
		RegisterInto(fs)
	} else {
		if len(defaultList) > 0 {
			RegisterInto(fs, defaultList...)
		}
		if len(extras) > 0 {
			RegisterInto(fs, extras...)
		}
	}
	fs.Parse(args)
}

// Register registers the command-line flags for the given flag names.
// Unlike Parse, it may be called multiple times.
// Passing zero names install all flags.
// Passing an unknown name triggers a panic.
//
// For example:
// 	flags.Register("config", "endpoint") // Register Config and Endpoint.
// or
// 	flags.Register() // Register all flags.
func Register(names ...string) {
	RegisterInto(flag.CommandLine, names...)
}

// RegisterInto  is the same as Register but accepts a FlagSet argument instead of
// using the default flag.CommandLine FlagSet.
func RegisterInto(fs *flag.FlagSet, names ...string) {
	if len(names) == 0 {
		// Register all flags if no names provided.
		for _, f := range flags {
			f.set(fs)
		}
	} else {
		for _, n := range names {
			f, ok := flags[n]
			if !ok {
				panic(fmt.Sprintf("unknown flag %q", n))
			}
			f.set(fs)
		}
	}
}

// Args returns a slice of -flag=value strings that will recreate
// the state of the flags. Flags set to their default value are elided.
func Args() []string {
	var args []string
	for _, f := range flags {
		arg := f.arg()
		if arg == "" {
			continue
		}
		args = append(args, arg)
		if f.arg2 != nil {
			args = append(args, f.arg2())
		}
	}
	return args
}

// strVar returns a flagVar for the given string flag.
func strVar(value *string, name, _default, usage string) *flagVar {
	return &flagVar{
		set: func(fs *flag.FlagSet) {
			fs.StringVar(value, name, _default, usage)
		},
		arg: func() string {
			return strArg(name, *value, _default)
		},
	}
}

// strArg returns a command-line argument that will recreate the flag,
// or the empty string if the value is the default.
func strArg(name, value, _default string) string {
	if value == _default {
		return ""
	}
	return "-" + name + "=" + value
}

// BlockSize is twinned to this implementation of flag.Value,
// allowing us to check the value when the flag is set.
type blockSizeFlag int

var blockSize blockSizeFlag

// String implements flag.Value.
func (f blockSizeFlag) String() string {
	return fmt.Sprint(int64(f))
}

// Set implements flag.Value.
func (f *blockSizeFlag) Set(size string) error {
	v, err := strconv.ParseInt(size, 0, 64)
	if err != nil {
		return err
	}
	if v <= 0 || v > maxBlockSize {
		return fmt.Errorf("block size %d out of range; maximum %d", v, maxBlockSize)
	}
	*f = blockSizeFlag(v)
	BlockSize = int(v)
	return nil
}

// Get implements flag.Getter.
func (f blockSizeFlag) Get() interface{} {
	return int(f)
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
