// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): support other kinds?

// Command upspinserver is a combined DirServer and StoreServer for use on
// stand-alone machines. It provides only the production implementations of the
// dir and store servers (dir/server and store/gcp).
package main

import (
	"flag"
	"net/http"
	"strings"

	"upspin.io/cloud/https"
	"upspin.io/config"
	dirServer "upspin.io/dir/server"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"
	storeServer "upspin.io/store/server"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"
	_ "upspin.io/pack/symm"

	// Load required transports
	_ "upspin.io/transports"
)

func main() {
	// TODO(adg): replace these flags with a server configuration file
	var (
		storeServerConfig []string
		storeCfgFile      = flag.String("store_config", "", "store config `file`")
		storeAddr         = flag.String("store_addr", "", "store `host:port`")
		dirServerConfig   []string
		dirCfgFile        = flag.String("dir_config", "", "directory config `file`")
		dirAddr           = flag.String("dir_addr", "", "directory `host:port`")
	)
	flag.Var(configFlag{&storeServerConfig}, "store_serverconfig", "store configuration")
	flag.Var(configFlag{&dirServerConfig}, "dir_serverconfig", "directory configuration")
	flags.Parse("https", "storeservername", "letscache", "log", "tls")

	// Parse configs.
	storeCfg, err := config.FromFile(*storeCfgFile)
	if err != nil {
		log.Fatal(err)
	}
	dirCfg, err := config.FromFile(*dirCfgFile)
	if err != nil {
		log.Fatal(err)
	}

	ready := make(chan struct{})

	// Set up StoreServer.
	store, err := storeServer.New(storeServerConfig...)
	if err != nil {
		log.Fatal(err)
	}
	store, err = perm.WrapStore(storeCfg, ready, store)
	if err != nil {
		log.Fatalf("Error wrapping store: %s", err)
	}

	// Set up DirServer.
	dir, err := dirServer.New(dirCfg, dirServerConfig...)
	if err != nil {
		log.Fatal(err)
	}
	if flags.StoreServerUser != "" {
		dir, err = perm.WrapDir(dirCfg, ready, upspin.UserName(flags.StoreServerUser), dir)
		if err != nil {
			log.Fatalf("Can't wrap DirServer monitoring %s: %s", flags.StoreServerUser, err)
		}
	} else {
		log.Printf("Warning: no Writers Group file protection -- all access permitted")
	}

	// Set up RPC server.
	httpStore := storeserver.New(storeCfg, store, upspin.NetAddr(*storeAddr))
	httpDir := dirserver.New(dirCfg, dir, upspin.NetAddr(*dirAddr))
	http.Handle("/api/Store/", httpStore)
	http.Handle("/api/Dir/", httpDir)

	// Set up HTTPS server.
	https.ListenAndServeFromFlags(ready, "upspinserver")
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
