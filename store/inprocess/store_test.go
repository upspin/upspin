// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

import (
	"testing"

	"upspin.io/errors"
)

func TestCapacity(t *testing.T) {
	const (
		testData1 = "123456789"
		testData2 = "01"
	)
	store, err := New("capacity=10")
	if err != nil {
		t.Fatal(err)
	}

	refData, err := store.Put([]byte(testData1))
	if err != nil {
		t.Fatal(err)
	}
	data, _, _, err := store.Get(refData.Reference)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testData1 {
		t.Fatalf("got = %s, want = %s", data, testData1)
	}
	_, err = store.Put([]byte(testData2))
	if !errors.Match(errStorageFull, err) {
		t.Fatalf("err = %s, want = %s", err, errStorageFull)
	}

	// Delete and try again.
	err = store.Delete(refData.Reference)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Put([]byte(testData2))
	if err != nil {
		t.Fatal(err)
	}
}
