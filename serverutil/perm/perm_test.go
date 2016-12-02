// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	owner  = "aly@example.com" // aly has keys in key/testdata/aly
	writer = "bob@uncle.com"   // bob has keys in key/testdata/bob

	groupDir    = owner + "/Group"
	ownersGroup = groupDir + "/" + WritersGroupFile
)

func TestNoGroupFileAllowsAll(t *testing.T) {
	ownerEnv, lookup := setup(t)
	perm, err := New(ownerEnv.Context, ownerEnv.Context.UserName(), lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}
	perm.Update()

	// Everyone is allowed.
	for _, user := range []upspin.UserName{
		owner,
		"writer",
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
	ownerEnv, lookup := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.Context, owner, lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}
	err = perm.Update()
	if err != nil {
		t.Fatal(err)
	}

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

func TestAllowsOthersAndWildcard(t *testing.T) {
	ownerEnv, lookup := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.MakeDirectory(groupDir)
	r.Put(ownersGroup, owner+","+writer+",*@superusers.com")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.Context, owner, lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}
	err = perm.Update()
	if err != nil {
		t.Fatal(err)
	}

	// Owner, writer and a wildcard users are allowed.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"master@superusers.com",
	} {
		if !perm.IsWriter(user) {
			t.Errorf("%s is not allowed, expected allowed", user)
		}
	}

	// No one else is allowed.
	for _, user := range []upspin.UserName{
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if perm.IsWriter(user) {
			t.Errorf("user %q is allowed; expected not allowed", user)
		}
	}

	ownerEnv.Exit()
}

func setup(t *testing.T) (*testenv.Env, LookupFunc) {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: owner,
		Packing:   upspin.PlainPack,
		Kind:      "inprocess",
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(name upspin.PathName) (*upspin.DirEntry, error) {
		return ownerEnv.Client.Lookup(name, true)
	}
	return ownerEnv, lookup
}
