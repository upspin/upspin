// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"upspin.io/context"
	"upspin.io/upspin"
)

// testSetup creates a temporary user context with inprocess services.
func testSetup(name string) (ctx upspin.Context, err error) {
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	ctx = context.New().
		SetUserName(upspin.UserName(name)).
		SetPacking(upspin.DebugPack).
		SetKeyEndpoint(endpoint).
		SetDirEndpoint(endpoint).
		SetStoreEndpoint(endpoint)
	publicKey := upspin.PublicKey(fmt.Sprintf("key for %s", name))
	user := &upspin.User{
		Name:      upspin.UserName(name),
		Dirs:      []upspin.Endpoint{ctx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: publicKey,
	}
	err = ctx.KeyServer().Put(user)
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
		// No free mountpiint found. Just pick one and hope we aren't
		// breaking another test.
		mountpoint = fmt.Sprintf("/tmp/upspinfstest%d", i)
		syscall.Unmount(mountpoint, 0)
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

	// Set up a user context.
	ctx, err := testSetup("tester@google.com")
	if err != nil {
		t.Fatal(err.Error())
	}

	// Mount the file system. It will be served in a separate go routine.
	do(ctx, mountpoint)

	// Run the tests.
	cmd := exec.Command("./test.sh", mountpoint, "tester@google.com")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()

	// Unmount.
	syscall.Unmount(mountpoint, 0)
	os.RemoveAll(mountpoint)

	// Report error.
	if err != nil {
		t.Fatal(err.Error())
	}
}
