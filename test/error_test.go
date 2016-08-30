// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"log"
	"testing"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

func TestErrors(t *testing.T) {
	setup := testenv.Setup{
		OwnerName: ownerName,
		Cleanup:   cleanup,
		Kind:      "inprocess",
		Packing:   upspin.PlainPack,
	}
	env, err := testenv.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	_, readerContext, err := env.NewUser(readerName)
	if err != nil {
		t.Fatal(err)
	}

	r := newRunner()
	r.AddUser(env.Context)
	r.AddUser(readerContext)

	// Create a simple tree.
	const (
		base    = ownerName + "/errors"
		dir     = base + "/dir"
		file    = dir + "/file"
		access  = dir + "/Access"
		content = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	r.Put(file, content)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// We expect a "not exist" error for a reader that has no rights
	// to the file, as they cannot even know that it exists.
	r.As(readerName)
	r.Get(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	putAccess := func(right, user string) {
		r.As(ownerName)
		r.Put(access, right+":"+user)
		if r.Failed() {
			// Not really expecting this to fail.
			t.Fatalf("%v:%v: %v", right, user, r.Diag())
		}
	}

	// Put in an access file for the owner only
	// and try the same reader check again.
	putAccess("*", ownerName)
	r.As(readerName)
	r.Get(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Give the reader the a specific (non-read) right and
	// it should see a permission error.
	for _, right := range []string{"list", "write", "create", "delete"} {
		putAccess(right, readerName)
		r.As(readerName)
		r.Get(file)
		if !r.Match(errors.E(errors.Permission)) {
			t.Fatalf("%s: %s", right, r.Diag())
		}
	}

	// Give the reader the "read" right and it works.
	putAccess("read", readerName)
	r.As(readerName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// TODO: put all of the stuff below somewhere more central

const (
	ownerName  = "upspin-test@google.com"
	readerName = "upspin-friend-test@google.com"
)

func cleanup(env *testenv.Env) error {
	dir, err := bind.DirServer(env.Context, env.Context.DirEndpoint())
	if err != nil {
		return err
	}

	fileSet1, err := dir.Glob(ownerName + "/*/*")
	if err != nil {
		return err
	}
	fileSet2, err := dir.Glob(ownerName + "/*")
	if err != nil {
		return err
	}
	entries := append(fileSet1, fileSet2...)
	var firstErr error
	deleteNow := func(name upspin.PathName) {
		_, err = dir.Delete(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("cleanup: error deleting %q: %v", name, err)
		}
	}
	// First, delete all Access files,
	// so we don't lock ourselves out if our tests above remove delete rights.
	for _, entry := range entries {
		if access.IsAccessFile(entry.Name) {
			deleteNow(entry.Name)
		}
	}
	for _, entry := range entries {
		if !access.IsAccessFile(entry.Name) {
			deleteNow(entry.Name)
		}
	}
	return firstErr
}
