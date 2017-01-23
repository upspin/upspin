// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"reflect"
	"testing"
)

const (
	size     = 800
	ref      = "ref"
	packdata = "packdata"
)

var block = &DirBlock{
	Location: Location{
		Endpoint: Endpoint{
			Transport: Remote,
			NetAddr:   NetAddr("example.com"),
		},
		Reference: Reference(ref),
	},
	Offset:   123,
	Size:     size,
	Packdata: []byte(packdata),
}

func TestCopyBlock(t *testing.T) {
	cp := block.Copy()
	if !reflect.DeepEqual(cp, block) {
		t.Fatalf("Expected equal: %+v\n%+v\n", cp, block)
	}

	// Modify fields in the copy to ensure it's really a copy.
	cp.Packdata[0] = 0xee
	cp.Location.Reference = Reference("wrong")
	cp.Size = 32

	// Verify original did not change.
	if block.Size != size {
		t.Errorf("got = %d, want = %d", block.Size, size)
	}
	if block.Location.Reference != Reference(ref) {
		t.Errorf("got = %s, want = %s", block.Location.Reference, ref)
	}
	if string(block.Packdata) != packdata {
		t.Errorf("got = %s, want = %s", block.Packdata, packdata)
	}
}

func TestCopyEntry(t *testing.T) {
	const (
		name          = "foo@bar.com/dir/file.txt"
		entryPackdata = "entry packdata"
		writer        = "foo@bar.com"
	)

	e := &DirEntry{
		Name:       name,
		SignedName: name,
		Packing:    EEPack,
		Blocks:     []DirBlock{*block},
		Packdata:   []byte(entryPackdata),
		Writer:     writer,
		Attr:       AttrDirectory,
		Sequence:   17,
	}

	cp := e.Copy()
	if !reflect.DeepEqual(cp, e) {
		t.Fatalf("Expected equal: %+v\n%+v\n", cp, e)
	}

	// Modify fields in the copy to ensure it's really a copy.
	cp.Packdata[0] = 0xab
	cp.Name = "foo"
	cp.Blocks[0].Packdata[0] = 0xff
	cp.Writer = "Shakespeare"

	// Verify original did not change.
	if string(e.Packdata) != entryPackdata {
		t.Errorf("got = %s, want = %s", e.Packdata, entryPackdata)
	}
	if e.Name != name {
		t.Errorf("got = %s, want = %s", e.Name, name)
	}
	if !reflect.DeepEqual(e.Blocks[0], *block) {
		t.Errorf("got = %v, want = %v", e.Blocks[0], block)
	}
	if e.Writer != writer {
		t.Errorf("got = %s, want = %s", e.Writer, writer)
	}
}
