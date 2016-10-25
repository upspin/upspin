// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	owner  = "bob@example.com" // bob has keys in key/testdata/bob.
	writer = "carla@writer.io" // carla has keys in key/testdata/carla.

	groupDir    = owner + "/Group"
	ownersGroup = groupDir + "/" + StoreWritersGroupFile
)

func TestNoGroupFileAllowsAll(t *testing.T) {
	ownerEnv := setup(t)
	store := WrapStore(ownerEnv.Context, ownerEnv.StoreServer)

	// Everyone is allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !store.perm.isWriter(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}

	ownerEnv.Exit()
}

func TestAllowsOnlyOwner(t *testing.T) {
	ownerEnv := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	store := WrapStore(ownerEnv.Context, ownerEnv.StoreServer)

	// Owner is allowed.
	if !store.perm.isWriter(owner) {
		t.Errorf("Owner is not allowed, expected allowed")
	}

	// No one else is allowed.
	for _, user := range []upspin.UserName{
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if store.perm.isWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}

	ownerEnv.Exit()
}

func TestIncludeRemoteGroups(t *testing.T) {
	ownerEnv := setup(t)
	writerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: writer,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(writerEnv.Context)

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

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, ownersContents)
	r.Put(otherGroupFile, otherGroupContents)

	r.As(writer)
	r.MakeDirectory(writerGroupDir)
	r.Put(writerAccessFile, writerAccessContents)
	r.Put(writerFamilyGroupFile, writerFamilyGroupContents)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	store := WrapStore(ownerEnv.Context, ownerEnv.StoreServer)

	// owner, writer and randomDude are allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		randomDude,
	} {
		if !store.perm.isWriter(user) {
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
		if store.perm.isWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}

	writerEnv.Exit()
	ownerEnv.Exit()
}

func TestLifeCycle(t *testing.T) {
	ownerEnv := setup(t)
	writerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: writer,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(writerEnv.Context)
	store := WrapStore(ownerEnv.Context, ownerEnv.StoreServer)

	// Everyone is allowed at first.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !store.perm.isWriter(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, "*@example.com") // Anyone at example.com is allowed.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Force a re-computation of the permissions.
	err = store.update()
	if err != nil {
		t.Fatal(err)
	}

	// Owner continues to be allowed, as well as others in the domain.
	for _, user := range []upspin.UserName{
		owner,
		"fred@example.com",
		"shirley@example.com",
	} {
		if !store.perm.isWriter(user) {
			t.Errorf("User %s is not allowed, expected allowed", user)
		}
	}

	// But no one else is allowed.
	for _, user := range []upspin.UserName{
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if store.perm.isWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}

	writerEnv.Exit()
	ownerEnv.Exit()
}

func TestIntegration(t *testing.T) {
	ownerEnv := setup(t)
	writerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: writer,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(writerEnv.Context)
	ownerStore := WrapStore(ownerEnv.Context, ownerEnv.StoreServer)

	// Dial the server for writer.
	srv, err := ownerStore.Dial(writerEnv.Context, writerEnv.Context.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	writerStore := srv.(upspin.StoreServer)

	// Everyone is allowed at first.
	for _, store := range []upspin.StoreServer{
		ownerStore,
		writerStore,
	} {
		ref, err := store.Put([]byte("data"))
		if err != nil {
			t.Fatal(err)
		}
		err = store.Delete(ref.Reference)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Allow only owner.
	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Force re-reading permissions file.
	err = ownerStore.update()
	if err != nil {
		t.Fatal(err)
	}

	// Writing as owner succeeds.
	ref1, err := ownerStore.Put([]byte("123"))
	if err != nil {
		t.Fatal(err)
	}

	// Writing as other fails.
	_, err = writerStore.Put([]byte("456"))
	expectedErr := errors.E(errors.Permission, upspin.UserName(writer))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
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

func setup(t *testing.T) *testenv.Env {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: owner,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerEnv
}
