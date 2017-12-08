// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"crypto/rand"
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	_ "upspin.io/cloud/storage/disk"
	"upspin.io/upspin"
)

func TestList(t *testing.T) {
	base, err := ioutil.TempDir("", "upspin-storage-disk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	store, err := New("backend=Disk", "basePath="+base)
	if err != nil {
		t.Fatal(err)
	}

	getRefs := func(token string) (refs upspin.ListRefsResponse) {
		b, _, _, err := store.Get(upspin.ListRefsMetadata + upspin.Reference(token))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &refs); err != nil {
			t.Fatal(err)
		}
		return refs
	}

	// Test list of an empty store.
	refs := getRefs("")
	if len(refs.Refs) != 0 {
		t.Errorf("list of empty bucket returned %d refs", len(refs.Refs))
	}
	if refs.Next != "" {
		t.Errorf("list of empty bucket returned non-empty page token %q", refs.Next)
	}

	const (
		fileSize       = 1024
		maxRefsPerCall = 1000 // Mirrored from cloud/storage/disk.
		pageTwoRefs    = 1
		nFiles         = maxRefsPerCall + pageTwoRefs
	)

	// Add some files.
	for i := 0; i < nFiles; i++ {
		_, err = store.Put(randomBytes(fileSize))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Test paginated list of populated store.
	seen := make(map[upspin.Reference]bool)
	refs = getRefs("")
	if len(refs.Refs) != maxRefsPerCall {
		t.Errorf("got %d refs, want %d", len(refs.Refs), maxRefsPerCall)
	}
	for _, ref := range refs.Refs {
		if seen[ref.Ref] {
			t.Errorf("saw duplicate ref %q", ref.Ref)
		}
		seen[ref.Ref] = true
		if ref.Size != fileSize {
			t.Errorf("ref %q has size %d, want %d", ref.Ref, ref.Size, fileSize)
		}
	}
	if refs.Next == "" {
		t.Errorf("got empty page token, want non-empty")
	}

	// Get the second and final page.
	refs = getRefs(refs.Next)
	if len(refs.Refs) != 1 {
		t.Errorf("got %d refs, want %d", len(refs.Refs), pageTwoRefs)
	}
	for _, ref := range refs.Refs {
		if seen[ref.Ref] {
			t.Errorf(" saw duplicate ref %q", ref.Ref)
		}
		seen[ref.Ref] = true
		if ref.Size != fileSize {
			t.Errorf(" ref %q has size %d, want %d", ref.Ref, ref.Size, fileSize)
		}
	}
	if refs.Next != "" {
		t.Errorf("got page token %q, want empty", refs.Next)
	}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
