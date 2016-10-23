// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"testing"

	"upspin.io/access"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	owner  = "bob@example.com" // bob has keys in key/testdata/bob.
	writer = "carla@writer.io" // carla has keys in key/testdata/carla.

	groupDir = owner + "/Group"
)

func TestNoGroupFileAllowsAll(t *testing.T) {
	ownerEnv := setup(t)
	perm := newPerm(ownerEnv.Context)
	perm.firstRun.Done() // Pretend we're doen and found nothing.

	// Everyone is allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !perm.isAllowedMutation(user) {
			t.Errorf("user %q is not allowed; expected allowed", user)
		}
	}
}

func TestAllowsOnlyOwner(t *testing.T) {
	ownerEnv := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	netAddr, err := cleanNetAddr(ownerEnv.Context.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}

	var (
		ownersGroup = upspin.PathName(groupDir + "/" + netAddr)
	)

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm := newPerm(ownerEnv.Context)
	perm.startUpdateLoop()
	perm.firstRun.Wait()

	// Only owner is allowed.
	if !perm.isAllowedMutation(owner) {
		t.Errorf("Owner is not allowed, expected allowed")
	}

	// No one else is allowed.
	for _, user := range []upspin.UserName{
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if perm.isAllowedMutation(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}
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

	netAddr, err := cleanNetAddr(ownerEnv.Context.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}

	var (
		ownersGroup = upspin.PathName(groupDir + "/" + netAddr)
	)

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

	perm := newPerm(ownerEnv.Context)
	perm.startUpdateLoop()
	perm.firstRun.Wait()

	// owner, writer and randomDude are allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		randomDude,
	} {
		if !perm.isAllowedMutation(user) {
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
		if perm.isAllowedMutation(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}
}

func setup(t *testing.T) *testenv.Env {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: owner,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
		//		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerEnv
}
