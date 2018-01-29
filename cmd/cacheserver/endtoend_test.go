// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/client/clientutil"
	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/path"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	inprocessdirserver "upspin.io/dir/inprocess"
	inprocesskeyserver "upspin.io/key/inprocess"
	inprocessstoreserver "upspin.io/store/inprocess"
)

var (
	inprocessEndpoint = upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
)

// Test basic cacheserver function. It uses a cacheserver and a
// remote dir/store server both running in this process and listening
// on local tcp ports.
func TestCache(t *testing.T) {
	errorOut := func(err error) {
		os.RemoveAll(flags.CacheDir)
		t.Fatal(err)
	}

	// The client and all servers will run as the same user.
	cfg := config.New()
	cfg = config.SetUserName(cfg, upspin.UserName("tester@google.com"))
	cfg = config.SetPacking(cfg, upspin.EEPack)

	// Use an inprocess key server.
	cfg = config.SetKeyEndpoint(cfg, inprocessEndpoint)
	bind.RegisterKeyServer(upspin.InProcess, inprocesskeyserver.New())

	cfg = setCertPool(cfg)

	var err error
	cfg, err = setUpFactotum(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = putUserToKeyServer(cfg, &inprocessEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	sep, err := startCombinedServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = putUserToKeyServer(cfg, sep)
	if err != nil {
		t.Fatal(err)
	}
	cep, err := startCacheServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, cl := newClient(cfg, sep, cep)

	// Create a root directory.
	root := upspin.PathName(cfg.UserName())
	if _, err := cl.MakeDirectory(root); err != nil {
		errorOut(err)
	}

	// Put something and read it back.
	fn := path.Join(root, "quux")
	str := "tada"
	if _, err := cl.Put(fn, []byte(str)); err != nil {
		errorOut(err)
	}
	data, err := cl.Get(fn)
	if err != nil {
		errorOut(err)
	}
	if string(data) != str {
		errorOut(fmt.Errorf("expected %q got %q", str, data))
	}

	// Make sure we can remove it.
	if err := cl.Delete(fn); err != nil {
		errorOut(err)
	}
	if _, err := cl.Get(fn); err == nil {
		errorOut(fmt.Errorf("file persisted beyond delete"))
	}

	// Force a cache flush and make sure we get the expected response.
	// This doesn't check functionality of the flush.
	loc := upspin.Location{
		Endpoint:  *sep,
		Reference: upspin.FlushWritebacksMetadata,
	}
	data, err = clientutil.ReadLocation(cfg, loc)
	if err != nil {
		errorOut(err)
	}
	flushReply := "cache flushed"
	if string(data) != flushReply {
		errorOut(fmt.Errorf("expected %q got %q", flushReply, string(data)))
	}

	// Remove the cache files and logs.
	os.RemoveAll(flags.CacheDir)
}

// setUpFactotum adds a factotum with the default test keys.
func setUpFactotum(cfg upspin.Config) (upspin.Config, error) {
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "user1")) // Always use user1's keys.
	if err != nil {
		return nil, err
	}

	return config.SetFactotum(cfg, f), nil
}

// putUserToKeyServer adds the user to the key server with ep as both its
// directory and store server endpoints.
func putUserToKeyServer(cfg upspin.Config, ep *upspin.Endpoint) (upspin.Config, error) {
	cfg = config.SetStoreEndpoint(cfg, *ep)
	cfg = config.SetDirEndpoint(cfg, *ep)
	user := &upspin.User{
		Name:      cfg.UserName(),
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: cfg.Factotum().PublicKey(),
	}
	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		return cfg, err
	}
	err = key.Put(user)
	return cfg, err
}

// startCombinedServer starts a remote server using inprocess directory and store.
// It returns the endpoint to it.
func startCombinedServer(cfg upspin.Config) (*upspin.Endpoint, error) {
	cfg = config.SetStoreEndpoint(cfg, inprocessEndpoint)
	cfg = config.SetDirEndpoint(cfg, inprocessEndpoint)

	bind.RegisterStoreServer(upspin.InProcess, inprocessstoreserver.New())
	bind.RegisterDirServer(upspin.InProcess, inprocessdirserver.New(cfg))

	// Both dir and store servers are in memory.
	ss := storeserver.New(cfg, inprocessstoreserver.New(), "")
	ds := dirserver.New(cfg, inprocessdirserver.New(cfg), "")
	http.Handle("/api/Store/", ss)
	http.Handle("/api/Dir/", ds)

	port, err := testutil.PickPort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("localhost:%s", port)
	ep, _ := upspin.ParseEndpoint("remote," + addr)

	ready := make(chan struct{})
	go https.ListenAndServe(ready, &https.Options{Addr: addr})
	<-ready
	return ep, nil
}

// startCacheServer starts a cache server and returns its endpoint.
func startCacheServer(cfg upspin.Config) (*upspin.Endpoint, error) {
	cfg = setCertPool(cfg)

	// Find a free port.
	port, err := testutil.PickPort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("localhost:%s", port)
	ep, _ := upspin.ParseEndpoint("remote," + addr)

	// Create a directory for the cacheserver's log and data.
	flags.CacheDir, err = ioutil.TempDir("", "cacheserver")
	if err != nil {
		return nil, err
	}

	if _, err = serve(cfg, addr); err != nil {
		os.RemoveAll(flags.CacheDir)
		return nil, err
	}
	return ep, nil
}

// newClient returns a client using the given servers and cache.
func newClient(cfg upspin.Config, server, cache *upspin.Endpoint) (upspin.Config, upspin.Client) {
	cfg = setCertPool(cfg)
	cfg = config.SetStoreEndpoint(cfg, *server)
	cfg = config.SetDirEndpoint(cfg, *server)
	cfg = config.SetCacheEndpoint(cfg, *cache)
	return cfg, client.New(cfg)
}

// setCertPool adds trusted certs to the Config.
func setCertPool(cfg upspin.Config) upspin.Config {
	dir := testutil.Repo("rpc", "testdata")
	return config.SetValue(cfg, "tlscerts", dir)
}
