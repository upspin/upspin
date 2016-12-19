// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): roll storeserver and dirserver into this?
// TODO(adg): support other kinds

// Command upspinserver is a combined DirServer and StoreServer
// for use on stand-alone VMs.
package main

import (
	"flag"
	"net/http"
	"strings"

	"google.golang.org/grpc"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/dir/server"
	"upspin.io/flags"
	"upspin.io/grpc/dirserver"
	"upspin.io/grpc/storeserver"
	"upspin.io/log"
	"upspin.io/serverutil/perm"
	"upspin.io/store/gcp"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

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
	var (
		storeConfig  []string
		storeCtxFile = flag.String("store_context", "", "store context `file`")
		storeAddr    = flag.String("store_addr", "", "store `host:port`")
		dirConfig    []string
		dirCtxFile   = flag.String("dir_context", "", "directory context `file`")
		dirAddr      = flag.String("dir_addr", "", "directory `host:port`")

		// TODO(adg): put this into flags package
		letsCache = flag.String("letsencrypt_cache", "", "letsencrypt cache `file`")
	)
	flag.Var(configFlag{&storeConfig}, "store_config", "store configuration")
	flag.Var(configFlag{&dirConfig}, "dir_config", "directory configuration")
	flags.Parse("https", "storeservername", "log", "tls")

	// Parse contexts.
	storeCtx, err := context.FromFile(*storeCtxFile)
	if err != nil {
		log.Fatal(err)
	}
	dirCtx, err := context.FromFile(*dirCtxFile)
	if err != nil {
		log.Fatal(err)
	}

	// Set up GRPC server.
	grpcServer := grpc.NewServer()
	ready := make(chan struct{})

	// Set up StoreServer.
	store, err := gcp.New(storeConfig...)
	if err != nil {
		log.Fatal(err)
	}
	store, err = perm.WrapStore(storeCtx, ready, store)
	if err != nil {
		log.Fatalf("Error wrapping store: %s", err)
	}
	storeSecureServer, err := grpcauth.NewSecureServer(grpcServer, auth.Config{Lookup: auth.PublicUserKeyService(storeCtx)})
	if err != nil {
		log.Fatal(err)
	}
	proto.RegisterStoreServer(grpcServer, storeserver.New(storeCtx, store, storeSecureServer, upspin.NetAddr(*storeAddr)))

	// Set up DirServer.
	dir, err := server.New(dirCtx, dirConfig...)
	if err != nil {
		log.Fatal(err)
	}
	if flags.StoreServerUser != "" {
		dir, err = perm.WrapDir(dirCtx, ready, upspin.UserName(flags.StoreServerUser), dir)
		if err != nil {
			log.Fatalf("Can't wrap DirServer monitoring %s: %s", flags.StoreServerUser, err)
		}
	} else {
		log.Printf("Warning: no Writers Group file protection -- all access permitted")
	}
	dirSecureServer, err := grpcauth.NewSecureServer(grpcServer, auth.Config{Lookup: auth.PublicUserKeyService(dirCtx)})
	if err != nil {
		log.Fatal(err)
	}
	proto.RegisterDirServer(grpcServer, dirserver.New(dirCtx, dir, dirSecureServer, upspin.NetAddr(*dirAddr)))

	// Set up HTTPS server.
	http.Handle("/", grpcServer)
	https.ListenAndServe(ready, "upspind", flags.HTTPSAddr, &https.Options{
		LetsEncryptCache: *letsCache,
		CertFile:         flags.TLSCertFile,
		KeyFile:          flags.TLSKeyFile,
	})
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
