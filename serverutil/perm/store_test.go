// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

func setupStoreEnv(t *testing.T) (store upspin.StoreServer, perm *Perm, ownerEnv *testenv.Env, wait, cleanup func()) {
	ownerEnv = setupEnv(t)
	store, err := bind.StoreServer(ownerEnv.Config, ownerEnv.Config.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	perm, wait, done := newWithEnv(t, ownerEnv)
	cleanup = func() {
		ownerEnv.Exit()
		done()
	}
	store = perm.WrapStore(store)
	return
}

func TestStoreNoGroupFileAllowsAll(t *testing.T) {
	_, perm, _, wait, cleanup := setupStoreEnv(t)
	defer cleanup()

	wait()

	// Everyone is allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !perm.IsWriter(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}
}

func TestStoreAllowsOnlyOwner(t *testing.T) {
	_, perm, ownerEnv, wait, cleanup := setupStoreEnv(t)
	defer cleanup()

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup Writers.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	wait() // Update call
	wait() // Watch event

	// Owner is allowed.
	if !perm.IsWriter(owner) {
		t.Errorf("Owner is not allowed, expected allowed")
	}

	// No one else is allowed.
	for _, user := range []upspin.UserName{
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if perm.IsWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}
}

func TestStoreIncludeRemoteGroups(t *testing.T) {
	ownerEnv := setupEnv(t)
	defer ownerEnv.Exit()

	writerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: writer,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writerEnv.Exit()

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)
	r.AddUser(writerEnv.Config)

	const (
		randomDude = "random@dude.io"

		ownersContents = owner + ", otherGroupFile"

		otherGroupFile     = groupDir + "/otherGroupFile"
		otherGroupContents = writer + "/Group/family"

		writerGroupDir            = writer + "/Group"
		writerAccessFile          = writer + "/Group/Access"
		writerAccessContents      = "r: " + access.All
		writerFamilyGroupFile     = writer + "/Group/family"
		writerFamilyGroupContents = writer + "," + randomDude
	)

	r.As(writer)
	r.MakeDirectory(writerGroupDir)
	r.Put(writerAccessFile, writerAccessContents)
	r.Put(writerFamilyGroupFile, writerFamilyGroupContents)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup Writers.
	r.MakeDirectory(groupDir)
	r.Put(otherGroupFile, otherGroupContents)
	r.Put(writersGroup, ownersContents)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, wait, done := newWithConfig(t, ownerEnv.Config)
	defer done()
	wait() // Update call
	wait() // Watch event

	// owner, writer and randomDude are allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		randomDude,
	} {
		if !perm.IsWriter(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}

	// No one else is allowed.
	for _, user := range []upspin.UserName{
		"all@upspin.io",
		"foo@bar.com",
		"god@heaven.infinite",
		"nobody@nobody.org",
	} {
		if perm.IsWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}
}

func TestStoreLifeCycle(t *testing.T) {
	_, perm, ownerEnv, wait, cleanup := setupStoreEnv(t)
	defer cleanup()

	writerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: writer,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)
	r.AddUser(writerEnv.Config)

	wait()

	// Everyone is allowed at first.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !perm.IsWriter(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup Writers.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, "*@example.com") // Anyone at example.com is allowed.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	wait()

	// Owner continues to be allowed, as well as others in the domain.
	for _, user := range []upspin.UserName{
		owner,
		"fred@example.com",
		"shirley@example.com",
	} {
		if !perm.IsWriter(user) {
			t.Errorf("User %s is not allowed, expected allowed", user)
		}
	}

	// But no one else is allowed.
	for _, user := range []upspin.UserName{
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if perm.IsWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}

	writerEnv.Exit()
}

func TestStoreIntegration(t *testing.T) {
	ownerStore, _, ownerEnv, wait, cleanup := setupStoreEnv(t)
	defer cleanup()

	writerConfig, err := ownerEnv.NewUser(writer)
	if err != nil {
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)

	wait()

	// Dial the same server endpoint for writer.
	srv, err := ownerStore.Dial(writerConfig, ownerEnv.Config.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	writerStore := srv.(upspin.StoreServer)

	// Check writing and deleting when there are several writers.
	ref, err := ownerStore.Put([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	err = ownerStore.Delete(ref.Reference)
	if err != nil {
		t.Fatal(err)
	}
	ref, err = writerStore.Put([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	err = writerStore.Delete(ref.Reference)
	if err == nil {
		t.Fatal("non-owner writer should not be able to delete")
	}

	// Allow only owner.
	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup Writers.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	wait()

	// Writing as owner succeeds.
	ref1, err := ownerStore.Put([]byte("123"))
	if err != nil {
		t.Fatal(err)
	}

	// Writing as other fails.
	_, err = writerStore.Put([]byte("456"))
	expectedErr := errors.E(errors.Permission, upspin.UserName(writer))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Deleting as other fails.
	err = writerStore.Delete(ref1.Reference)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}

	// Deleting as owner succeeds.
	err = ownerStore.Delete(ref1.Reference)
	if err != nil {
		t.Fatal(err)
	}
}
