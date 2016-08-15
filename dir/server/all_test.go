// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/upspin"

	_ "upspin.io/key/inprocess"
	_ "upspin.io/pack/ee"
	_ "upspin.io/store/inprocess"
)

const (
	userName   = "fred@flintstone.org"
	serverName = "dirserver@server.com"
)

var testDir string

func TestMakeRoot(t *testing.T) {
	s := newDirServerForTesting(t)
	de, err := s.MakeDirectory(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := de.Name, upspin.PathName(userName+"/"); got != want {
		t.Errorf("de.Name = %q, want = %q", got, want)
	}
	// Lookup confirms the de we got.
	deLookup, err := s.Lookup(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := deLookup.Name, de.Name; got != want {
		t.Errorf("deLookup.Name = %v, want = %v", got, want)
	}
	// And we can't make a new root again.
	_, err = s.MakeDirectory(userName + "/")
	expectedErr := errors.E(errors.Exist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %q, want = %q", err, expectedErr)
	}
}

func TestPut(t *testing.T) {
	s := newDirServerForTesting(t)
	de := &upspin.DirEntry{
		Name:    userName + "/file1.txt",
		Attr:    upspin.AttrNone,
		Writer:  userName,
		Packing: upspin.PlainPack,
	}
	_, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMakeDirectory(t *testing.T) {
	s := newDirServerForTesting(t)
	_, err := s.MakeDirectory(userName + "/dir")
	if err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	var err error
	testDir, err = ioutil.TempDir("", "DirServer")
	if err != nil {
		panic(err)
	}

	code := m.Run()

	os.RemoveAll(testDir)
	os.Exit(code)
}

func newDirServerForTesting(t *testing.T) *server {
	factotum, err := factotum.New(repo("key/testdata/upspin-test"))
	if err != nil {
		t.Fatal(err)
	}
	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	cxt := context.New().
		SetFactotum(factotum).
		SetUserName(serverName).
		SetStoreEndpoint(endpointInProcess).
		SetKeyEndpoint(endpointInProcess).
		SetPacking(upspin.EEPack)
	key := cxt.KeyServer()
	// Set the public key for the tree, since it must do Auth against the Store.
	user := &upspin.User{
		Name:      serverName,
		Dirs:      []upspin.Endpoint{cxt.DirEndpoint()},
		Stores:    []upspin.Endpoint{cxt.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		panic(err)
	}

	// Set the public key for the user, since EE Pack requires the dir owner
	// to have a wrapped key.
	user = &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{cxt.DirEndpoint()},
		Stores:    []upspin.Endpoint{cxt.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		panic(err)
	}

	return &server{
		serverContext: cxt,
		userContext:   context.New().SetUserName(userName),
		logDir:        testDir,
	}
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		panic("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
