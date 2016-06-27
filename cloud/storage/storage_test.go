// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage_test

import (
	"testing"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/storagetest"
)

func TestRegister(t *testing.T) {
	err := storage.Register("dummy", &storagetest.DummyStorage{})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.Register("dummy", &storagetest.DummyStorage{})
	if err == nil {
		t.Fatalf("Duplicate registration should fail.")
	}
	s, err := storage.Dial("dummy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("Expected non-nil.")
	}
}
