// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"

	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

// testCopyEntries tests that servers return copies of data instead of
// references to their internal data structures.
func testCopyEntries(t *testing.T, r *testenv.Runner) {
	const (
		dir           = ownerName + "/dir-entry-copy"
		file          = dir + "/file"
		content       = "file contents"
		entryPackdata = "corrupted"
		blockPackdata = "clobbered"
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
	clobberPackdata(r.Entry.Packdata)
	clobberBlocks(r.Entry.Blocks)

	// Get the contents of dir and mess up the blocks. If the server
	// sees the messed up data, it may crash or complain.
	r.DirLookup(upspin.PathName(dir))
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// Clobber important fields.
	r.Entry.Packing = upspin.EEIntegrityPack
	clobberPackdata(r.Entry.Packdata)
	clobberBlocks(r.Entry.Blocks)

	// Read the file again. Verify it's all intact.
	r.DirLookup(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Entry.Name != file {
		t.Fatalf("entry name is %q, want %q", r.Entry.Name, file)
	}
	if r.Entry.Sequence == -20 {
		t.Error("successfully clobbered sequence field")
	}
	if string(r.Entry.Packdata) == entryPackdata {
		t.Error("successfully clobbered entry packdata field")
	}
	if len(r.Entry.Blocks) == 0 {
		t.Fatal("expected 1 or more blocks, got zero")
	}
	if string(r.Entry.Blocks[0].Packdata) == blockPackdata {
		t.Error("successfully clobbered block[0] packdata field")
	}
}

func clobberPackdata(packdata []byte) {
	for i := range packdata {
		packdata[i] = packdata[i] + byte(i)
	}
}

func clobberBlocks(blocks []upspin.DirBlock) {
	for i := range blocks {
		clobberPackdata(blocks[i].Packdata)
	}
}
