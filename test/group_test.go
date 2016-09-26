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
	setup1 := &testenv.Setup{
		OwnerName: ownerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	}
	setup2 := &testenv.Setup{
		OwnerName: readerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	}
	setup3 := &testenv.Setup{
		OwnerName: middleName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	}
	env1, err := testenv.New(setup1)
	if err != nil {
		t.Fatal(err)
	}
	env2, err := testenv.New(setup2)
	if err != nil {
		t.Fatal(err)
	}
	env3, err := testenv.New(setup3)
	if err != nil {
		t.Fatal(err)
	}

	// Assert env1, env2 and env3 talk to different DirServers.
	if env1.Context.DirEndpoint() == env2.Context.DirEndpoint() {
		t.Fatalf("env1 and env2 endpoints are the same, expected distinct: %v", env1.Context.DirEndpoint())
	}
	if env1.Context.DirEndpoint() == env3.Context.DirEndpoint() {
		t.Fatalf("env1 and env3 endpoints are the same, expected distinct: %v", env1.Context.DirEndpoint())
	}
	if env2.Context.DirEndpoint() == env3.Context.DirEndpoint() {
		t.Fatalf("env2 and env3 endpoints are the same, expected distinct: %v", env2.Context.DirEndpoint())
	}

	r := testenv.NewRunner()
	r.AddUser(env1.Context)
	r.AddUser(env2.Context)
	r.AddUser(env3.Context)

	const (
		base           = ownerName + "/grouptest"
		file           = base + "/test"
		access         = base + "/Access"
		groupBase      = ownerName + "/Group"
		contentsOfFile = "tadda!"
	)

	// Owner creates a root and Group file.
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(groupBase)
	r.Put(groupBase+"/myclique", readerName+"/Group/team")
	r.Put(file, contentsOfFile)
	r.Put(access, "r:"+testenv.TestServerName)

	// Reader creates a root and a Group file.
	r.As(readerName)
	r.MakeDirectory(readerName + "/Group")
	r.Put(readerName+"/Group/team", middleName)
	r.Put(readerName+"/Access", "r:"+testenv.TestServerName)

	// MiddleName tries to access a file by owner, without success.
	r.As(middleName)
	r.Get(file)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Now owner adds myclique to the Access file, allowing middleName
	// indirectly to see file.
	r.As(ownerName)
	r.Put(access, "r,l: myclique")

	// And now middleName should have access since middleName is listed
	// indirectly in readerName's team Group file.
	r.As(middleName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if string(r.Data) != contentsOfFile {
		t.Fatalf("got = %q, want = %q", r.Data, contentsOfFile)
	}
}
