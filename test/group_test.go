// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

// This file tests access permissions using Group files spread over different
// DirServers.

// TODO: this only tests dir/server. Add inprocess when it's supported there.

import (
	"testing"

	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const middleName = "joe@middleman.com" // joe has keys in key/testdata.

func TestGroupFileMultiDir(t *testing.T) {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: ownerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	readerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: readerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	middleEnv, err := testenv.New(&testenv.Setup{
		OwnerName: middleName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Assert env1, env2 and env3 talk to different DirServers.
	if ownerEnv.Context.DirEndpoint() == readerEnv.Context.DirEndpoint() {
		t.Fatalf("ownerEnv and readerEnv endpoints are the same, expected distinct: %v", ownerEnv.Context.DirEndpoint())
	}
	if ownerEnv.Context.DirEndpoint() == middleEnv.Context.DirEndpoint() {
		t.Fatalf("ownerEnv and middleEnv endpoints are the same, expected distinct: %v", ownerEnv.Context.DirEndpoint())
	}
	if readerEnv.Context.DirEndpoint() == middleEnv.Context.DirEndpoint() {
		t.Fatalf("readerEnv and middleEnv endpoints are the same, expected distinct: %v", readerEnv.Context.DirEndpoint())
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(readerEnv.Context)
	r.AddUser(middleEnv.Context)

	const (
		base              = ownerName + "/group-multidir-test"
		file              = base + "/test"
		ownerAccess       = base + "/Access"
		ownerGroup        = ownerName + "/Group"
		ownerGroupClique  = ownerGroup + "/clique"
		ownerGroupAccess  = ownerGroup + "/Access"
		readerGroup       = readerName + "/Group"
		readerGroupTeam   = readerGroup + "/team"
		readerGroupAccess = readerGroup + "/Access"
		fileContent       = "tadda!"
	)

	// Owner creates a root and Group file.
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(ownerGroup)
	r.Put(ownerGroupClique, readerGroupTeam)
	r.Put(file, fileContent)
	r.Put(ownerAccess, "r:"+ownerName)
	r.Put(ownerGroupAccess, "r:all")

	// Reader creates a root and a Group file and gives the dirserver
	// read rights.
	r.As(readerName)
	r.MakeDirectory(readerGroup)
	r.Put(readerGroupTeam, middleName)
	r.Put(readerGroupAccess, "r:all")

	// MiddleName tries to access a file by owner, without success.
	r.As(middleName)
	r.Get(file)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Now owner adds clique to the Access file, allowing middleName
	// indirectly to see file.
	r.As(ownerName)
	r.Put(ownerAccess, "r,l:clique")

	// And now middleName should have access since middleName is listed
	// indirectly in readerName's team Group file.
	r.As(middleName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != fileContent {
		t.Fatalf("got = %q, want = %q", r.Data, fileContent)
	}
}
