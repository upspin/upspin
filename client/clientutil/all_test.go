// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package clientutil

// TODO: test with EEPack; check more error conditions.

import (
	"os"
	"path/filepath"
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/test/testfixtures"
	"upspin.io/upspin"

	_ "upspin.io/key/inprocess"
	_ "upspin.io/pack/plain"
)

const (
	userName = "bob@smith.com"
)

var (
	inProcess = upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	tLocs []upspin.Location
)

func TestReadAll(t *testing.T) {
	ctx := setupTestContext(t)
	store := &mockStore{
		locWithContent: tLocs[9],
		content:        []byte("found it!"),
		locRedirection: map[upspin.Reference][]upspin.Location{
			tLocs[0].Reference: []upspin.Location{tLocs[1], tLocs[2], tLocs[3]},
			tLocs[3].Reference: []upspin.Location{tLocs[4], tLocs[2], tLocs[5]},
			tLocs[5].Reference: []upspin.Location{tLocs[6], tLocs[7], tLocs[8]},
			tLocs[8].Reference: []upspin.Location{tLocs[9]},
		},
	}
	err := bind.RegisterStoreServer(upspin.InProcess, store)
	if err != nil {
		t.Fatal(err)
	}
	entry := &upspin.DirEntry{
		Name:    userName + "/testfile",
		Attr:    upspin.AttrNone,
		Packing: upspin.PlainPack,
		Blocks: []upspin.DirBlock{
			{
				Offset:   0,
				Size:     int64(len(store.content)),
				Location: tLocs[0],
			},
		},
		Writer:   userName,
		Sequence: upspin.SeqBase,
	}

	got, err := ReadAll(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(store.content) {
		t.Errorf("got = %q, want = %s", got, store.content)
	}
}

func setupTestContext(t *testing.T) upspin.Context {
	// Create some test locations.
	for i := 0; i < 10; i++ {
		loc := upspin.Location{
			Endpoint:  inProcess,
			Reference: upspin.Reference("ref" + string(i)),
		}
		tLocs = append(tLocs, loc)
	}

	f, err := factotum.New(repo("bob"))
	if err != nil {
		t.Fatal(err)
	}
	context := context.New().
		SetUserName(userName).
		SetDirEndpoint(inProcess).
		SetKeyEndpoint(inProcess).
		SetStoreEndpoint(inProcess).
		SetPacking(upspin.EEPack).
		SetFactotum(f)

	key, err := bind.KeyServer(context, context.KeyEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	user := &upspin.User{
		Name:      upspin.UserName(userName),
		Dirs:      []upspin.Endpoint{context.DirEndpoint()},
		Stores:    []upspin.Endpoint{context.StoreEndpoint()},
		PublicKey: f.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}
	return context
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("client/clientutil: no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/key/testdata/"+dir)
}

type mockStore struct {
	testfixtures.DummyStoreServer
	locWithContent upspin.Location
	content        []byte
	locRedirection map[upspin.Reference][]upspin.Location
}

func (s *mockStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	if locs, found := s.locRedirection[ref]; found {
		return nil, locs, nil
	}
	if ref == s.locWithContent.Reference {
		return s.content, nil, nil
	}
	return nil, nil, errors.E(errors.NotExist)
}

func (s *mockStore) Dial(upspin.Context, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}
