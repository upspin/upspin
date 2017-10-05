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
	env := setupEnv(t)
	defer env.Exit()

	r := testenv.NewRunner()
	r.AddUser(env.Config)
	r.As(owner)
	r.Put(accessFile, "r,l:all\n*:"+owner) // Permission for anyone to read and list, owner has all rights.
	r.MakeDirectory(groupDir)
	r.Put(writersGroup, owner) // Only owner allowed to create roots.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	perm, wait, done := newWithEnv(t, env)
	defer done()
	wait()
	wait()

	// Create a new user without creating its root.
	writerCtx, err := env.NewUser(writer)
	if err != nil {
		t.Fatal(err)
	}
	// Dial the DirServer as writer.
	dir, err := bind.DirServer(env.Config, env.Config.DirEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := perm.WrapDir(dir).Dial(writerCtx, writerCtx.DirEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	dir = svc.(upspin.DirServer)

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
