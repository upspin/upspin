// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"testing"
	"upspin.io/errors"
	"upspin.io/store/perm"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	owner = "bob@example.com" // bob has keys in key/testdata/bob.
	other = "carla@writer.io" // carla has keys in key/testdata/carla.

	groupDir    = owner + "/Group"
	ownersGroup = groupDir + "/" + perm.StoreWritersGroupFile
)

func TestPermissionLifeCycle(t *testing.T) {
	ownerEnv, writerEnv, store := setup(t)

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(writerEnv.Context)

	storeOwner := store.server

	// Writing as owner succeeds.
	ref1, err := storeOwner.Put([]byte("123"))
	if err != nil {
		t.Fatal(err)
	}

	// Dial for other.
	srv, err := storeOwner.Dial(writerEnv.Context, writerEnv.Context.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	storeOther := srv.(upspin.StoreServer)

	// Writing as other succeeds.
	ref2, err := storeOther.Put([]byte("456"))
	if err != nil {
		t.Fatal(err)
	}
	// Deleting as other succeeds.
	err = storeOther.Delete(ref2.Reference)
	if err != nil {
		t.Fatal(err)
	}

	// Allow only owner.
	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Force re-reading permissions file.
	err = storeOwner.perm.UpdateAllowedWriters()
	if err != nil {
		t.Fatal(err)
	}

	// Writing as owner succeeds.
	_, err = storeOwner.Put([]byte("123"))
	if err != nil {
		t.Fatal(err)
	}

	// Writing as other fails.
	_, err = storeOther.Put([]byte("456"))
	expectedErr := errors.E(errors.Permission, upspin.UserName(other))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}

	// Deleting as other fails.
	err = storeOther.Delete(ref1.Reference)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}

	// Deleting as owner succeeds.
	err = storeOwner.Delete(ref1.Reference)
	if err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (ownerEnv, writerEnv *testenv.Env, store *storeTestServer) {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: owner,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}
	store = newStoreServerForContext(ownerEnv.Context)
	writerEnv, err = testenv.New(&testenv.Setup{
		OwnerName: other,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerEnv, writerEnv, store
}
