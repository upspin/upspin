// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): support other kinds?

// Command upspinserver is a combined DirServer and StoreServer for use on
// stand-alone machines. It provides only the production implementations of the
// dir and store servers (dir/server and store/gcp).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/errors"

	"upspin.io/client"
	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/dir/server"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"
	"upspin.io/store/gcp"
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

var (
	cfgPath = flag.String("serverconfig", filepath.Join(os.Getenv("HOME"), "upspin/server"), "server configuration `directory`")
	ready   = make(chan struct{})
)

func main() {
	flags.Parse("https", "log")
	flags.LetsEncryptCache = filepath.Join(*cfgPath, "letsencrypt")

	err := initServer()
	if err == noConfig {
		log.Print("Configuration file not found. Running in setup mode.")
		http.HandleFunc("/", setupHandler)
	} else if err != nil {
		log.Fatal(err)
	}

	// Set up HTTPS server.
	https.ListenAndServeFromFlags(ready, "upspinserver")
}

var noConfig = errors.Str("no configuration")

func initServer() error {
	serverConfig, err := readServerConfig()
	if os.IsNotExist(err) {
		return noConfig
	} else if err != nil {
		return err
	}

	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(*cfgPath, "serviceaccount.json"))

	cfg := config.New()
	cfg = config.SetUserName(cfg, serverConfig.User)

	f, err := factotum.NewFromDir(*cfgPath)
	if err != nil {
		return err
	}
	cfg = config.SetFactotum(cfg, f)

	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   serverConfig.Addr,
	}
	cfg = config.SetDirEndpoint(cfg, ep)
	cfg = config.SetStoreEndpoint(cfg, ep)

	storeCfg := config.SetPacking(cfg, upspin.EEIntegrityPack)
	dirCfg := config.SetPacking(cfg, upspin.SymmPack)

	// Set up StoreServer.
	store, err := gcp.New("gcpBucketName="+serverConfig.Bucket, "defaultACL=publicRead")
	if err != nil {
		return err
	}
	store, err = perm.WrapStore(storeCfg, ready, store)
	if err != nil {
		return fmt.Errorf("error wrapping store: %s", err)
	}

	// Set up DirServer.
	logDir := filepath.Join(*cfgPath, "dirserver-logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	dir, err := server.New(dirCfg, "userCacheSize=1000", "logDir="+logDir)
	if err != nil {
		return err
	}
	dir, err = perm.WrapDir(dirCfg, ready, serverConfig.User, dir)
	if err != nil {
		return fmt.Errorf("Can't wrap DirServer monitoring %s: %s", flags.StoreServerUser, err)
	}

	// Set up RPC server.
	httpStore := storeserver.New(storeCfg, store, serverConfig.Addr)
	httpDir := dirserver.New(dirCfg, dir, serverConfig.Addr)
	http.Handle("/api/Store/", httpStore)
	http.Handle("/api/Dir/", httpDir)

	log.Println("Store and Directory servers initialized.")

	go func() {
		if err := setupWriters(storeCfg); err != nil {
			log.Printf("Error creating Writers file: %v", err)
		}
	}()
	return nil
}

var (
	setupDone = false
	setupMu   sync.Mutex
)

func setupHandler(w http.ResponseWriter, r *http.Request) {
	setupMu.Lock()
	defer setupMu.Unlock()
	if setupDone {
		http.NotFound(w, r)
		return
	}

	switch r.URL.Path {
	case "/":
		fmt.Fprint(w, "Unconfigured Upspin Server")
		return
	default:
		http.NotFound(w, r)
		return
	case "/setupserver":
	}

	// TODO: check for POST
	files := map[string][]byte{}
	if err := json.NewDecoder(r.Body).Decode(&files); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(*cfgPath, 0700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, name := range configureServerFiles {
		body, ok := files[name]
		if !ok {
			http.Error(w, fmt.Sprintf("missing config file %q", name), http.StatusBadRequest)
			return
		}
		err := ioutil.WriteFile(filepath.Join(*cfgPath, name), body, 0600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			os.RemoveAll(*cfgPath)
			return
		}
	}
	if err := initServer(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK")

	setupDone = true
}

func setupWriters(cfg upspin.Config) error {
	writers, err := ioutil.ReadFile(filepath.Join(*cfgPath, "Writers"))
	if err != nil {
		return err
	}

	c := client.New(cfg)

	dir := upspin.PathName(cfg.UserName())
	if err := existsOK(c.MakeDirectory(dir)); err != nil {
		return err
	}

	dir += "/Group"
	if err := existsOK(c.MakeDirectory(dir)); err != nil {
		return err
	}

	file := dir + "/Writers"
	_, err = c.Put(file, writers)
	return err
}

func existsOK(_ *upspin.DirEntry, err error) error {
	if errors.Match(errors.E(errors.Exist), err) {
		return nil
	}
	return err
}

func readServerConfig() (*ServerConfig, error) {
	cfgFile := filepath.Join(*cfgPath, serverConfigFile)
	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}
	cfg := &ServerConfig{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Keep the following declarations in sync with cmd/upspin/setupserver.go.

type ServerConfig struct {
	Addr   upspin.NetAddr
	User   upspin.UserName
	Bucket string
}

const serverConfigFile = "serverconfig.json"

var configureServerFiles = []string{
	"Writers",
	"public.upspinkey",
	"secret.upspinkey",
	"serverconfig.json",
	"serviceaccount.json",
	"symmsecret.upspinkey",
}
