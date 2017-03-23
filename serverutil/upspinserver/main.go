// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package upspinserver is a combined DirServer and StoreServer for use on
// stand-alone machines. It provides only the production implementations of the
// dir and store servers (dir/server and store/server).
package upspinserver // import "upspin.io/serverutil/upspinserver"

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"upspin.io/client"
	"upspin.io/cloud/https"
	"upspin.io/config"
	dirServer "upspin.io/dir/server"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"
	storeServer "upspin.io/store/server"
	"upspin.io/subcmd"
	"upspin.io/upspin"

	// Packers.
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	// Required transports.
	_ "upspin.io/transports"
)

var (
	cfgPath   = flag.String("serverconfig", filepath.Join(config.Home(), "upspin", "server"), "server configuration `directory`")
	letsPath  = flag.String("letscache", filepath.Join(config.Home(), "upspin", "letsencrypt"), "Let's Encrypt cache `directory`")
	enableWeb = flag.Bool("web", false, "enable Upspin web interface")
	ready     = make(chan struct{})
)

func Main() {
	flags.Parse("https", "tls", "log")

	server, cfg, perm, err := initServer(startup)
	if err == noConfig {
		log.Print("Configuration file not found. Running in setup mode.")
		http.Handle("/", &setupHandler{})
	} else if err != nil {
		log.Fatal(err)
	} else {
		http.Handle("/", newWeb(cfg, perm))
	}

	// Set up HTTPS server.
	var opt https.Options
	if flags.TLSCertFile != "" && flags.TLSKeyFile != "" {
		opt.CertFile = flags.TLSCertFile
		opt.KeyFile = flags.TLSKeyFile
	} else {
		opt.LetsEncryptCache = *letsPath
		if server != nil {
			host, _, err := net.SplitHostPort(string(server.Addr))
			if err != nil {
				log.Printf("Error parsing addr from config %q: %v", server.Addr, err)
				log.Printf("Warning: Let's Encrypt certificates will be fetched for any host.")
			} else {
				opt.LetsEncryptHosts = []string{host}
			}
		}
	}
	https.ListenAndServe(ready, "upspinserver", flags.HTTPSAddr, &opt)
}

var noConfig = errors.Str("no configuration")

type initMode int

const (
	startup initMode = iota
	setupServer
)

func initServer(mode initMode) (*subcmd.ServerConfig, upspin.Config, *perm.Perm, error) {
	serverConfig, err := readServerConfig()
	if os.IsNotExist(err) {
		return nil, nil, nil, noConfig
	} else if err != nil {
		return nil, nil, nil, err
	}

	// TODO(adg): remove this once we have deprecated serviceaccount.json
	// and the Bucket field.
	if serverConfig.Bucket != "" {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(*cfgPath, "serviceaccount.json"))
	}

	cfg := config.New()
	cfg = config.SetUserName(cfg, serverConfig.User)

	f, err := factotum.NewFromDir(*cfgPath)
	if err != nil {
		return nil, nil, nil, err
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
	switch {
	case len(serverConfig.StoreConfig) > 0:
		// Use the provided configuration, if available.
		storeServerConfig = serverConfig.StoreConfig
	case serverConfig.Bucket != "":
		// Bucket configured, use Google Cloud Storage.
		// TODO(adg): remove this when the Bucket field is retired.
		storeServerConfig = []string{"backend=GCS", "gcpBucketName=" + serverConfig.Bucket, "defaultACL=publicRead"}
	default:
		// No bucket configured, use simple on-disk store.
		storagePath := filepath.Join(*cfgPath, "storage")
		storeServerConfig = []string{"backend=Disk", "basePath=" + storagePath}
	}
	store, err := storeServer.New(storeServerConfig...)
	if err != nil {
		return nil, nil, nil, err
	}

	// Set up DirServer.
	logDir := filepath.Join(*cfgPath, "dirserver-logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, nil, nil, err
	}
	dir, err := dirServer.New(dirCfg, "userCacheSize=1000", "logDir="+logDir)
	if err != nil {
		return nil, nil, nil, err
	}

	// Wrap store and dir with permission checking.
	perm := perm.NewWithDir(dirCfg, ready, serverConfig.User, dir)
	store = perm.WrapStore(store)
	dir = perm.WrapDir(dir)

	// Set up RPC server.
	httpStore := storeserver.New(storeCfg, store, serverConfig.Addr)
	httpDir := dirserver.New(dirCfg, dir, serverConfig.Addr)
	http.Handle("/api/Store/", httpStore)
	http.Handle("/api/Dir/", httpDir)

	log.Println("Store and Directory servers initialized.")
	log.Printf("Store server configuration: %s", fmtStoreConfig(storeServerConfig))

	if mode == setupServer {
		// Create Writers file if this was triggered by 'upspin setupserver'.
		go func() {
			if err := setupWriters(storeCfg); err != nil {
				log.Printf("Error creating Writers file: %v", err)
			}
		}()
	}
	return serverConfig, cfg, perm, nil
}

// fmtStoreConfig formats a ServerConfig.StoreConfig value as a string,
// ommitting any privateKeyData fields as they include sensitive information.
func fmtStoreConfig(cfg []string) string {
	var out []string
	for _, s := range cfg {
		if !strings.HasPrefix(s, "privateKeyData=") {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
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

	for _, name := range subcmd.SetupServerFiles {
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
	_, cfg, perm, err := initServer(setupServer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK")

	h.done = true
	h.web = newWeb(cfg, perm)
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

func readServerConfig() (*subcmd.ServerConfig, error) {
	cfgFile := filepath.Join(*cfgPath, subcmd.ServerConfigFile)
	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}
	cfg := &subcmd.ServerConfig{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
