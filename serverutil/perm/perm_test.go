// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"
	"time"

	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	owner  = "aly@example.com" // aly has keys in key/testdata/aly
	writer = "bob@uncle.com"   // bob has keys in key/testdata/bob

	accessFile    = owner + "/Access"
	accessContent = "r,l: " + testenv.TestServerName + "\n*: " + owner

	groupDir     = owner + "/Group"
	writersGroup = groupDir + "/" + WritersGroupFile
)

func TestNoGroupFileAllowsAll(t *testing.T) {
	ownerEnv := setup(t)
	perm, err := New(ownerEnv.DirServerContext, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}

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
	ownerEnv := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup the file.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.DirServerContext, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
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
	ownerEnv := setup(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup the file.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner+" "+writer+" *@superusers.com")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.DirServerContext, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}

	// Owner, writer and a wildcard user are allowed.
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

	// Remove everyone but owner. Update should be very fast, through the
	// Watch API.
	r.Put(writersGroup, owner)

	// Try 3 times to prove no one has access (it may take a while because
	// of update events).
	usersWithAccess := make(map[upspin.UserName]bool)
	for retries := 0; retries < 3; retries++ {
		for _, user := range []upspin.UserName{
			writer,
			"master@superusers.com",
			"foo@bar.com",
			"nobody@nobody.org",
		} {
			if perm.IsWriter(user) {
				usersWithAccess[user] = true
			} else {
				delete(usersWithAccess, user)
			}
		}
		if len(usersWithAccess) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(usersWithAccess) > 0 {
		t.Fatalf("These users had access, expected none had it: %v", usersWithAccess)
	}

	ownerEnv.Exit()
}

func setup(t *testing.T) *testenv.Env {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: owner,
		Packing:   upspin.PlainPack,
		Kind:      "server", // Must implement Watch API.
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerEnv
}
