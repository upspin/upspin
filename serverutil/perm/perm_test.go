// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/errors"
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

func TestCantFindFileAllowsAll(t *testing.T) {
	ownerEnv := setupEnv(t)
	perm, err := New(ownerEnv.Context, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}

	// Everyone is allowed, since we can't read the owner file.
	for _, user := range []upspin.UserName{
		owner,
		writer,
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if !perm.IsWriter(user) {
			t.Errorf("IsWriter(%q)=false, want true", user)
		}
	}

	ownerEnv.Exit()
}

func TestNoFileAllowsAll(t *testing.T) {
	ownerEnv := setupEnv(t)

	// Put a permissive Access file, now server knows the file is not there.
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup the file.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.Context, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
	if err != nil {
		t.Fatal(err)
	}

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
	ownerEnv := setupEnv(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup the file.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner can write.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.Context, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
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
	ownerEnv := setupEnv(t)
	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)

	r.As(owner)
	r.Put(accessFile, accessContent) // So server can lookup the file.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner+" "+writer+" *@superusers.com")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, err := New(ownerEnv.Context, owner, ownerEnv.DirServer.Lookup, ownerEnv.DirServer.Watch)
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

	saved := save(perm)

	// Remove everyone but owner. Update should be very fast, through the
	// Watch API.
	r.Put(writersGroup, owner)

	// Allow time for events to be delivered.
	err = wait(perm, saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []upspin.UserName{
		writer,
		"master@superusers.com",
		"foo@bar.com",
		"nobody@nobody.org",
	} {
		if perm.IsWriter(user) {
			t.Errorf("%s is allowed; expected not allowed", user)
		}
	}

	ownerEnv.Exit()
}

// save returns the current state of Perm's event counter.
func save(perm *Perm) int64 {
	perm.mu.Lock()
	defer perm.mu.Unlock()
	return perm.eventCounter
}

// wait waits until the event counter has moved beyond the mark value.
func wait(perm *Perm, mark int64) error {
	counter := mark
	for retries := 0; counter <= mark; retries++ {
		if retries > 10 {
			return errors.Str("waited too long for event that never happened")
		}
		perm.mu.Lock()
		perm.eventCond.Wait()
		counter = perm.eventCounter
		perm.mu.Unlock()
	}
	return nil
}

func setupEnv(t *testing.T) *testenv.Env {
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
