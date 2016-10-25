// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/access"
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
	perm := NewStore(ownerEnv.Context)

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

	perm := NewStore(ownerEnv.Context)

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

	perm := NewStore(ownerEnv.Context)

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
	perm := NewStore(ownerEnv.Context)

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
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, "*@example.com") // Anyone at example.com is allowed.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Force a re-computation of the permissions.
	err = perm.UpdateNow()
	if err != nil {
		t.Fatal(err)
	}

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
	ownerEnv.Exit()
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
