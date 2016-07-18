// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

import (
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/upspin"

	_ "upspin.io/dir/inprocess"
	_ "upspin.io/pack/debug"
	_ "upspin.io/store/inprocess"
)

var (
	userName = upspin.UserName("joe@blow.com")
)

func setup(t *testing.T) (upspin.KeyServer, upspin.Context) {
	c := context.New().SetUserName(userName).SetPacking(upspin.DebugPack)
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	u, err := bind.KeyServer(c, e)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKeyEndpoint(e)
	c.SetStoreEndpoint(e)
	c.SetDirEndpoint(e)
	return u, c
}

func TestInstallAndLookup(t *testing.T) {
	u, ctxt := setup(t)
	testKey, ok := u.(*Service)
	if !ok {
		t.Fatal("Not an inprocess KeyServer")
	}

	dir, err := bind.DirServer(ctxt, ctxt.DirEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	err = testKey.Install(userName, dir)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	user, err := u.Lookup(userName)
	eRecv := user.Dirs
	key := user.PublicKey
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if key != "" {
		t.Errorf("Expected no keys for user %v, got %q", userName, key)
	}
	if len(eRecv) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(eRecv))
	}
	if eRecv[0].Transport != upspin.InProcess {
		t.Errorf("Expected endpoint to be %d, but instead it was %d", upspin.InProcess, eRecv[0].Transport)
	}
}

func TestPublicKeysAndUsers(t *testing.T) {
	u, _ := setup(t)
	testKey, ok := u.(*Service)
	if !ok {
		t.Fatal("Not an inprocess KeyServer")
	}
	const testKeyStr = "pub key1"
	testKey.SetPublicKeys(userName, []upspin.PublicKey{
		upspin.PublicKey(testKeyStr),
	})

	user, err := u.Lookup(userName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if user.PublicKey == "" {
		t.Fatalf("Expected key for user %v, got nothing", userName)
	}
	if string(user.PublicKey) != testKeyStr {
		t.Errorf("Expected key %s, got %s", testKeyStr, user.PublicKey)
	}

	users := testKey.ListUsers()
	if len(users) != 1 {
		t.Fatalf("Expected 1 user, got %d", len(users))
	}
	if users[0] != userName {
		t.Errorf("Expected user %s, got %v", userName, users[0])
	}

	// Delete keys for user
	testKey.SetPublicKeys(userName, nil)

	users = testKey.ListUsers()
	if len(users) != 0 {
		t.Fatalf("Expected 0 users, got %d", len(users))
	}
}

func TestSafety(t *testing.T) {
	// Make sure the answers from Lookup are not aliases for the Service maps.
	u, _ := setup(t)
	testKey, ok := u.(*Service)
	if !ok {
		t.Fatal("Not an inprocess KeyServer")
	}
	const testKeyStr = "pub key2"
	testKey.SetPublicKeys(userName, []upspin.PublicKey{
		upspin.PublicKey(testKeyStr),
	})

	user0, err := u.Lookup(userName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(user0.Dirs) != 1 || user0.PublicKey == "" {
		t.Fatal("Extra locs or missing key")
	}

	// Save and then modify the two.
	loc0 := user0.Dirs[0]
	user0.Dirs[0].Transport++
	key0 := user0.PublicKey
	user0.PublicKey += "gotcha"

	// Fetch again, expect the original results.
	user1, err := u.Lookup(userName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(user1.Dirs) != 1 || user1.PublicKey == "" {
		t.Fatal("Extra locs or missing key (1)")
	}
	if user1.Dirs[0] != loc0 {
		t.Error("loc was modified")
	}
	if user1.PublicKey != key0 {
		t.Error("key was modified")
	}
}
