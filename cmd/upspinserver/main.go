// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): support other kinds?

// Command upspinserver is a combined DirServer and StoreServer for use on
// stand-alone machines. It provides only the production implementations of the
// dir and store servers (dir/server and store/gcp).
package main // import "upspin.io/cmd/upspinserver"

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/errors"

	"upspin.io/client"
	"upspin.io/cloud/https"
	"upspin.io/config"
	dirServer "upspin.io/dir/server"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"
	storeServer "upspin.io/store/server"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/transports"
)

var (
	cfgPath   = flag.String("serverconfig", filepath.Join(os.Getenv("HOME"), "upspin/server"), "server configuration `directory`")
	letsPath  = flag.String("letscache", filepath.Join(os.Getenv("HOME"), "upspin/letsencrypt"), "Let's Encrypt cache `directory`")
	enableWeb = flag.Bool("web", false, "enable Upspin web interface")
	ready     = make(chan struct{})
)

func main() {
	flags.Parse("https", "log")

	server, cfg, err := initServer(startup)
	if err == noConfig {
		log.Print("Configuration file not found. Running in setup mode.")
		http.Handle("/", &setupHandler{})
	} else if err != nil {
		log.Fatal(err)
	} else {
		http.Handle("/", newWeb(cfg))
	}

	// Set up HTTPS server.
	opt := &https.Options{
		LetsEncryptCache: *letsPath,
	}
	if server != nil {
		host, _, err := net.SplitHostPort(string(server.Addr))
		if err != nil {
			log.Printf("Error parsing addr from config %q: %v", server.Addr, err)
			log.Printf("Warning: Let's Encrypt certificates will be fetched for any host.")
		} else {
			opt.LetsEncryptHosts = []string{host}
		}
	}
	https.ListenAndServe(ready, "upspinserver", flags.HTTPSAddr, opt)
}

var noConfig = errors.Str("no configuration")

type initMode int

const (
	startup initMode = iota
	setupServer
)

func initServer(mode initMode) (*ServerConfig, upspin.Config, error) {
	serverConfig, err := readServerConfig()
	if os.IsNotExist(err) {
		return nil, nil, noConfig
	} else if err != nil {
		return nil, nil, err
	}

	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(*cfgPath, "serviceaccount.json"))

	cfg := config.New()
	cfg = config.SetUserName(cfg, serverConfig.User)

	f, err := factotum.NewFromDir(*cfgPath)
	if err != nil {
		return nil, nil, err
	}
	cfg = config.SetFactotum(cfg, f)

	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   serverConfig.Addr,
	}
	cfg = config.SetDirEndpoint(cfg, ep)
	cfg = config.SetStoreEndpoint(cfg, ep)

	storeCfg := config.SetPacking(cfg, upspin.EEIntegrityPack)
	dirCfg := config.SetPacking(cfg, upspin.EEPack)

	// Set up StoreServer.
	var storeServerConfig []string
	storagePath := filepath.Join(*cfgPath, "storage")
	if serverConfig.Bucket != "" {
		// Bucket configured, use Google Cloud Storage.
		storeServerConfig = []string{"backend=GCS", "gcpBucketName=" + serverConfig.Bucket, "defaultACL=publicRead"}
	} else {
		// No bucket configured, use simple on-disk store.
		storeServerConfig = []string{"backend=Disk", "basePath=" + storagePath}
	}
	store, err := storeServer.New(storeServerConfig...)
	if err != nil {
		return nil, nil, err
	}
	store, err = perm.WrapStore(storeCfg, ready, store)
	if err != nil {
		return nil, nil, fmt.Errorf("error wrapping store: %s", err)
	}

	// Set up DirServer.
	logDir := filepath.Join(*cfgPath, "dirserver-logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, nil, err
	}
	dir, err := dirServer.New(dirCfg, "userCacheSize=1000", "logDir="+logDir)
	if err != nil {
		return nil, nil, err
	}
	dir, err = perm.WrapDir(dirCfg, ready, serverConfig.User, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("Can't wrap DirServer monitoring %s: %s", flags.StoreServerUser, err)
	}

	// Set up RPC server.
	httpStore := storeserver.New(storeCfg, store, serverConfig.Addr)
	httpDir := dirserver.New(dirCfg, dir, serverConfig.Addr)
	http.Handle("/api/Store/", httpStore)
	http.Handle("/api/Dir/", httpDir)

	log.Println("Store and Directory servers initialized.")

	if b := serverConfig.Bucket; b != "" {
		log.Printf("Storing data in the Google Cloud Storage bucket %q", b)
	} else {
		log.Printf("Storing data under %s", storagePath)
	}

	if mode == setupServer {
		// Create Writers file if this was triggered by 'upspin setupserver'.
		go func() {
			if err := setupWriters(storeCfg); err != nil {
				log.Printf("Error creating Writers file: %v", err)
			}
		}()
	}
	return serverConfig, cfg, nil
}

type setupHandler struct {
	mu   sync.Mutex
	done bool
	web  http.Handler
}

func (h *setupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.done {
		web := h.web
		h.mu.Unlock()
		if web != nil {
			web.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	defer h.mu.Unlock()

	switch r.URL.Path {
	case "/":
		fmt.Fprint(w, "Unconfigured Upspin Server")
		return
	default:
		http.NotFound(w, r)
		return
	case "/setupserver":
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// The rest of this function is the setupserver handler.
	}

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
	_, cfg, err := initServer(setupServer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK")

	h.done = true
	h.web = newWeb(cfg)
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

// existsOK returns err if it is not an errors.Exist error,
// in which case it returns nil.
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
// TODO(adg): move these to their own package if/when there are more users.

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
}
