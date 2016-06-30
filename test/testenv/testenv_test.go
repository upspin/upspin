// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testenv

import (
	"testing"

	"upspin.io/bind"
	"upspin.io/upspin"

	_ "upspin.io/directory/inprocess"
	_ "upspin.io/store/inprocess"
)

func TestInProcess(t *testing.T) {
	const (
		userName = "testuser@testdomain.com"
		content2 = "yo! file2"
	)
	testSetup := &Setup{
		OwnerName: upspin.UserName(userName),
		Packing:   upspin.EEPack,
		KeyKind:   "p256",
		Transport: upspin.InProcess,
		Tree: Tree{
			E("Dir1/", ""),
			E("Dir1/file1.txt", "yo! file1"),
			E("Dir2/", ""),
			E("Dir2/file2.txt", content2),
		},
	}

	env, err := New(testSetup)
	if err != nil {
		t.Fatal(err)
	}

	// Now check the tree was setup correctly
	dir, err := bind.Directory(env.Context, env.Context.DirectoryEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	de, err := dir.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}
	expectedDirs := []upspin.PathName{upspin.PathName(userName + "/Dir1"), upspin.PathName(userName + "/Dir2")}
	if len(de) != len(expectedDirs) {
		t.Errorf("Expected %d dir entries, got %d", len(expectedDirs), len(de))
	}
	for i := 0; i < len(expectedDirs); i++ {
		if de[i].Name != expectedDirs[i] {
			t.Errorf("Expected entry %s, got %s", expectedDirs[i], de[i].Name)
		}
	}
	data, err := env.Client.Get(upspin.PathName(userName + "/Dir2/file2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content2 {
		t.Errorf("Expected content %q, got %q", content2, data)
	}
}
