// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package disk

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"

	"upspin.io/cloud/storage"
	"upspin.io/upspin"
)

func TestList(t *testing.T) {
	base, err := ioutil.TempDir("", "upspin-storage-disk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	opts := &storage.Opts{Opts: map[string]string{"basePath": base}}
	store, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}

	ls, ok := store.(storage.Lister)
	if !ok {
		t.Fatalf("%T does not implement storage.Lister", store)
	}

	// Test list of an empty store.
	refs, next, err := ls.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("list of empty bucket returned %d refs", len(refs))
	}
	if next != "" {
		t.Errorf("list of empty bucket returned non-empty page token %q", next)
	}

	// Test pagination by reducing the results per page.
	oldMaxRefsPerCall := maxRefsPerCall
	defer func() { maxRefsPerCall = oldMaxRefsPerCall }()
	maxRefsPerCall = 5

	const nFiles = 100 // Must be evenly divisible by maxRefsPerCall.
	pages := nFiles / maxRefsPerCall

	// Add some files.
	var testData = []byte("some file content")
	for i := 0; i < nFiles; i++ {
		err = store.Put(fmt.Sprintf("%x-test-%d", randomBytes(8), i), testData)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Test paginated list of populated store.
	seen := make(map[upspin.Reference]bool)
	for i := 0; i < pages; i++ {
		refs, next, err = ls.List(next)
		if err != nil {
			t.Fatal(err)
		}
		if len(refs) != maxRefsPerCall {
			t.Errorf("iteration %d: got %d refs, want %d", i, len(refs), maxRefsPerCall)
		}
		for _, ref := range refs {
			if seen[ref.Ref] {
				t.Errorf("iteration %d: saw duplicate ref %q", i, ref.Ref)
			}
			seen[ref.Ref] = true
			if got, want := ref.Size, int64(len(testData)); got != want {
				t.Errorf("iteration %d: ref %q has size %d, want %d", i, ref.Ref, got, want)
			}
		}
		if i == pages-1 {
			if next != "" {
				t.Errorf("iteration %d: got page token %q, want empty", i, next)
			}
		} else if next == "" {
			t.Fatalf("iteration %d: got empty page token, want non-empty", i)
		}
	}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
