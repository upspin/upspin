// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

// This file tests access permissions using Group files spread over different
// DirServers.

// TODO: this only tests dir/server. Add inprocess when it's supported there.

import (
	"testing"

	"upspin.io/errors"
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
	defer ownerEnv.Exit()
	readerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: readerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer readerEnv.Exit()
	middleEnv, err := testenv.New(&testenv.Setup{
		OwnerName: middleName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer middleEnv.Exit()

	// Assert env1, env2 and env3 talk to different DirServers.
	if ownerEnv.Config.DirEndpoint() == readerEnv.Config.DirEndpoint() {
		t.Fatalf("ownerEnv and readerEnv endpoints are the same, expected distinct: %v", ownerEnv.Config.DirEndpoint())
	}
	if ownerEnv.Config.DirEndpoint() == middleEnv.Config.DirEndpoint() {
		t.Fatalf("ownerEnv and middleEnv endpoints are the same, expected distinct: %v", ownerEnv.Config.DirEndpoint())
	}
	if readerEnv.Config.DirEndpoint() == middleEnv.Config.DirEndpoint() {
		t.Fatalf("readerEnv and middleEnv endpoints are the same, expected distinct: %v", readerEnv.Config.DirEndpoint())
	}

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)
	r.AddUser(readerEnv.Config)
	r.AddUser(middleEnv.Config)

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
		readAllPlusOwner  = "read:all\n*:" + ownerName
	)

	// Owner creates a root and Group file.
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(ownerGroup)
	r.Put(file, fileContent)
	r.Put(ownerAccess, "r:"+ownerName)
	r.Put(ownerGroupAccess, readAllPlusOwner)
	r.Put(ownerGroupClique, readerGroupTeam)

	// Reader creates a root and a Group file and gives the dirserver
	// read rights.
	r.As(readerName)
	r.MakeDirectory(readerGroup)
	r.Put(readerGroupAccess, "r:all")
	r.Put(readerGroupTeam, middleName)

	// MiddleName tries to access a file by owner, without success.
	r.As(middleName)
	r.Get(file)
	if !r.Match(errPrivate) {
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

func TestInvalidGroupName(t *testing.T) {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: ownerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerEnv.Exit()

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Config)

	const (
		base                  = ownerName + "/group-badname-test"
		ownerGroup            = ownerName + "/Group"
		ownerGroupBad         = ownerGroup + "/**"
		ownerGroupBadContents = "ann@example.com"
	)

	// Owner creates a root and tries to create invalidly named Group file.
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(ownerGroup)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.Put(ownerGroupBad, ownerGroupBadContents)
	err = r.Err()
	if err == nil {
		t.Fatalf("expected error putting Group file, got none")
	}
	if !errors.Is(errors.Invalid, err) {
		t.Fatal(err)
	}
}
