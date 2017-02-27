// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

import (
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func TestCapacity(t *testing.T) {
	store, err := New("capacity=10")
	if err != nil {
		t.Fatal(err)
	}

	var refs []*upspin.Refdata
	testData := []string{"12", "34", "56", "78", "90"}
	for i, d := range testData {
		refData, err := store.Put([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, refData)
		data, _, _, err := store.Get(refData.Reference)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != d {
			t.Fatalf("i: got = %s, want = %s", i, data, d)
		}
	}

	checkRefs := func() {
		for i, r := range refs {
			data, _, _, err := store.Get(r.Reference)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != testData[i] {
				t.Fatalf("%d: got = %s, want = %s", i, data, testData[i])
			}
		}
	}

	// Check that all refs are still around.
	checkRefs()

	// Now add something and check refs 0 and 1 had to be deleted.
	newRef, err := store.Put([]byte("777"))
	if err != nil {
		t.Fatal(err)
	}
	data, _, _, err := store.Get(newRef.Reference)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "777" {
		t.Fatalf("got = %s, want = 777", data)
	}

	// First two are now gone.
	for i := 0; i < 2; i++ {
		_, _, _, err := store.Get(refs[i].Reference)
		if !errors.Match(errors.E(errors.NotExist), err) {
			t.Fatalf("expected not exist, got = %s", err)
		}
	}

	// Verify the others are still there.
	refs = refs[2:]
	testData = testData[2:]
	checkRefs()
}
