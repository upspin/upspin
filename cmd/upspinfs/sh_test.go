// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

// +build !windows

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	dirserver "upspin.io/dir/inprocess"
	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"
)

// unmountHelper exists because (on Linux at least) you need an suid
// program to unmount.
func umountHelper(mountpoint string) error {
	cmd := exec.Command("umount", mountpoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// testSetup creates a temporary user config with inprocess services.
func testSetup(name string) (cfg upspin.Config, err error) {
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "user1")) // Always use user1's keys.
	if err != nil {
		panic("cannot initialize factotum: " + err.Error())
	}

	cfg = config.New()
	cfg = config.SetUserName(cfg, upspin.UserName(name))
	cfg = config.SetPacking(cfg, upspin.EEPack)
	cfg = config.SetKeyEndpoint(cfg, endpoint)
	cfg = config.SetStoreEndpoint(cfg, endpoint)
	cfg = config.SetDirEndpoint(cfg, endpoint)
	cfg = config.SetFactotum(cfg, f)

	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, storeserver.New())
	bind.RegisterDirServer(upspin.InProcess, dirserver.New(cfg))

	publicKey := upspin.PublicKey(fmt.Sprintf("key for %s", name))
	user := &upspin.User{
		Name:      upspin.UserName(name),
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: publicKey,
	}
	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		return nil, err
	}
	err = key.Put(user)
	return
}

func TestShell(t *testing.T) {
	// Create a mountpoint. There are 4 possible mountpoints /tmp/upsinfstest[1-4].
	// This lets us set up some /etc/fstab entries on Linux for the tests and
	// avoid using sudo.
	var err error
	var mountpoint string
	found := false
	for i := 1; i < 5; i++ {
		mountpoint = fmt.Sprintf("/tmp/upspinfstest%d", i)
		if err = os.Mkdir(mountpoint, 0777); err == nil {
			found = true
			break
		}
	}
	for i := 1; i < 5; i++ {
		// No free mountpoint found. Just pick one and hope we aren't
		// breaking another test.
		mountpoint = fmt.Sprintf("/tmp/upspinfstest%d", i)
		umountHelper(mountpoint)
		os.RemoveAll(mountpoint)
		if err = os.Mkdir(mountpoint, 0777); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Fatal(err.Error())
	}
	fmt.Printf("mountpoint is %s\n", mountpoint)

	// Set up a user config.
	cfg, err := testSetup("tester@google.com")
	if err != nil {
		t.Fatal(err.Error())
	}

	// A directory for cache files.
	cacheDir, err := ioutil.TempDir("/tmp", "upspincache")
	if err != nil {
		t.Fatal(err.Error())
	}

	// Mount the file system. It will be served in a separate go routine.
	do(cfg, mountpoint, cacheDir)

	// Run the tests.
	cmd := exec.Command("./test.sh", mountpoint, "tester@google.com")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()

	// Unmount.
	umountHelper(mountpoint)
	os.RemoveAll(mountpoint)
	os.RemoveAll(cacheDir)

	// Report error.
	if err != nil {
		t.Fatal(err.Error())
	}
}
