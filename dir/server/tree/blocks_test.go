// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"reflect"
	"testing"

	"upspin.io/upspin"
)

func TestLoadKidsFromBlock(t *testing.T) {
	dir := upspin.DirEntry{
		Name:       "foo@bar.com/dir",
		SignedName: "foo@bar.com/dir",
		Writer:     "writer@bar.com",
		Packing:    upspin.PlainPack,
		Packdata:   []byte("some pack data"),
		Attr:       upspin.AttrLink,
		Sequence:   17,
		Time:       1234567890,
	}
	emptyFile := upspin.DirEntry{
		Name:       "foo@bar.com/dir/file.txt",
		SignedName: "foo@bar.com/dir/file.txt",
		Writer:     "writer@bar.com",
		Packing:    upspin.PlainPack,
		Packdata:   []byte("some pack data"),
		Attr:       upspin.AttrLink,
		Sequence:   18,
		Time:       1234567891,
	}

	data, err := emptyFile.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	n := &node{
		entry: dir,
		// set dirty to test we do not allow loading kids when node is dirty
		dirty: true,
	}

	// load whild dirty should fail
	err = loadKidsFromBlock(n, data)
	if err == nil {
		t.Errorf("expected error, got nil")
	}

	// clear dirty
	n.dirty = false
	err = loadKidsFromBlock(n, data)
	if err != nil {
		t.Fatalf("failed to load block %v", err)
	}

	var (
		k  *node
		ok bool
	)
	if k, ok = n.kids["file.txt"]; !ok {
		t.Fatal("no kids after loading")
	}

	if !reflect.DeepEqual(k.entry, emptyFile) {
		t.Errorf("got = %v, want %v", k.entry, emptyFile)
	}

	// load again should fail
	err = loadKidsFromBlock(n, data)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}
