// Clobberright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"

	"upspin.io/test/testenv"
)

func testDirEntryClobber(t *testing.T, r *testenv.Runner) {
	const (
		dir     = ownerName + "/dir-entry-copy"
		file    = dir + "/file"
		content = "file contents"
	)

	r.As(ownerName)
	r.MakeDirectory(dir)
	r.Put(file, content)
	r.DirLookup(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Entry.Name != file {
		t.Fatalf("entry name is %q, want %q", r.Entry.Name, file)
	}
	if len(r.Entry.Blocks) == 0 {
		t.Fatal("expected 1 or more blocks, got zero")
	}

	// Mutating the entry that we received should not affect
	// the copy that the server has.
	r.Entry.Sequence = -20
	//r.Entry.Blocks[0].Offset = -1

	r.DirLookup(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Entry.Name != file {
		t.Fatalf("entry name is %q, want %q", r.Entry.Name, file)
	}
	if r.Entry.Sequence == -20 {
		t.Fatal("successfully clobbered sequence field")
	}

}
