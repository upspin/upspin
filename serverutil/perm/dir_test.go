// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package perm

import (
	"testing"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

func TestDirIntegration(t *testing.T) {
	ownerEnv, wait, cleanup := setupEnv(t)
	defer cleanup()

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)
	r.As(owner)
	r.Put(accessFile, "r,l:all\n*:"+owner) // Permission for anyone to read and list, owner has all rights.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner allowed to create roots.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Create a new user without creating its root.
	writerCtx, err := ownerEnv.NewUser(writer)
	if err != nil {
		t.Fatal(err)
	}

	dirServer, err := bind.DirServerFor(writerCtx, writer)
	if err != nil {
		t.Fatal(err)
	}
	// Wrap the writer's DirServer, pointing Perm to the owner's group file.
	dir := WrapDir(writerCtx, readyNow, owner, dirServer)
	wait()
	wait()

	// At first, only owner can create a root, so it fails.
	entry := &upspin.DirEntry{
		Name:       writer + "/",
		SignedName: writer + "/",
		Attr:       upspin.AttrDirectory,
	}
	_, err = dir.Put(entry)
	expectedErr := errors.E(errors.Permission, upspin.UserName(writer))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Allow writer to create a root now.
	r.Put(writersGroup, owner+" "+writer)
	wait()

	_, err = dir.Put(entry)
	if err != nil {
		t.Fatalf("Expected root creation to succeed; instead err = %s", err)
	}
}
