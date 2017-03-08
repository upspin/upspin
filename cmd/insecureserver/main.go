package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/log"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil/perm"

	dirServer "upspin.io/dir/server"
	storeServer "upspin.io/store/server"

	// Load useful packers and transports.
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"
	_ "upspin.io/transports"
)

const (
	upspinRoot = "/tmp/upspinroot"
	logDir     = "/tmp/logdir"
	serverUser = "edpin@edpin.com"
)

var (
	upspinCfgDir = flag.String("config", ".", "path to upspin config file")
	port         = flag.String("port", ":8080", "meh")
)

func main() {
	ready := make(chan struct{})
	err := startUpspinServer(ready)
	if err != nil {
		panic(err)
	}

	log.Printf("Starting up...")
	defaultOptions := &https.Options{
		CertFile: filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/rpc/testdata/cert.pem"),
		KeyFile:  filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/rpc/testdata/key.pem"),
	}

	https.ListenAndServe(ready, "", *port, defaultOptions)
}

func startUpspinServer(ready chan struct{}) error {
	cfg, err := config.FromFile(filepath.Join(*upspinCfgDir, "config"))
	if err != nil {
		return err
	}

	// Set up StoreServer.
	store, err := storeServer.New("backend=Disk", "basePath="+upspinRoot)
	if err != nil {
		return err
	}
	// Set up DirServer.
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	dir, err := dirServer.New(cfg, "userCacheSize=1000", "logDir="+logDir)
	if err != nil {
		return err
	}
	// Wrap store and dir with permission checking.
	perm := perm.NewWithDir(cfg, ready, cfg.UserName(), dir)
	store = perm.WrapStore(store)
	dir = perm.WrapDir(dir)

	// Set up RPC server.
	httpStore := storeserver.New(cfg, store, cfg.StoreEndpoint().NetAddr)
	httpDir := dirserver.New(cfg, dir, cfg.DirEndpoint().NetAddr)
	http.Handle("/api/Store/", httpStore)
	http.Handle("/api/Dir/", httpDir)
	return nil
}
