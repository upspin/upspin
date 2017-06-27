// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"fmt"
	"reflect"
	"testing"
)

var dirEnt = DirEntry{
	Name:       "u@foo.com/a/directory",
	SignedName: "u@foo.com/a/directory",
	Packing:    EEPack,
	Time:       123456,
	Blocks: []DirBlock{
		dirBlock1,
		dirBlock2,
	},
	Link:     "",
	Packdata: []byte{1, 2, 3, 4},
	Attr:     AttrDirectory, // Just so it's not zero; this is not a semantically valid entry.
	Sequence: 1234,
	Writer:   "u@foo.com",
}

var dirBlock1 = DirBlock{
	Location: Location{
		Endpoint: Endpoint{
			Transport: Remote,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Cinder",
	},
	Offset:   0,
	Size:     1024,
	Packdata: []byte("sign"),
}

var dirBlock2 = DirBlock{
	Location: Location{
		Endpoint: Endpoint{
			Transport: InProcess,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Ice",
	},
	Offset:   1024,
	Size:     4096,
	Packdata: []byte("nature"),
}

var linkDirEnt = DirEntry{
	Name:       "u@foo.com/a/link",
	SignedName: "u@foo.com/a/link",
	Link:       "v@bar.com/b/foo",
	Packing:    PlainPack,
	Time:       123456,
	Packdata:   nil, // Links have no packdata.
	Attr:       AttrLink,
	Sequence:   1234,
	Writer:     "u@foo.com",
}

var nameCombinations = []DirEntry{
	{Name: "", SignedName: ""},
	{Name: "user@example.com/path", SignedName: ""},
	{Name: "", SignedName: "user@example.com/path"},
	{Name: "user@example.com/path", SignedName: "user@example.com/path"},
	{Name: "user@example.com/path", SignedName: "user@example.com/different/path"},
}

func TestDirEntryMarshal(t *testing.T) {
	testDirEntryMarshal(t, "regular file", &dirEnt)
	testDirEntryMarshal(t, "link", &linkDirEnt)
	for _, e := range nameCombinations {
		testDirEntryMarshal(t, fmt.Sprintf("Name=%q SignedName=%q", e.Name, e.SignedName), &e)
	}
}

func testDirEntryMarshal(t *testing.T, msg string, entry *DirEntry) {
	data, err := entry.Marshal()
	if err != nil {
		t.Fatalf("Marshal %s: %v", msg, err)
	}
	var new DirEntry
	remaining, err := new.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal %s: %v", msg, err)
	}
	if len(remaining) != 0 {
		t.Errorf("%s: data remains after unmarshal", msg)
	}
	if !reflect.DeepEqual(entry, &new) {
		t.Errorf("%s: bad result. got:\n\t%+v\nwant:\n\t%+v", msg, &new, entry)
	}
}

func TestDirEntryMarshalClearsPackdata(t *testing.T) {
	src, dst := linkDirEnt, dirEnt

	data, err := src.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src, dst) {
		t.Errorf("bad result. got:\n\t%+v\nwant:\n\t%+v", dst, src)
	}
}

// Was a bug that Unmarshal would not clear the Block field of the receiver
// if the unmarshaled entry was of zero length.
func TestDirEntryMarshalClearsBlocks(t *testing.T) {
	full := dirEnt
	empty := dirEnt
	empty.Blocks = nil
	data, err := empty.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := full.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Error("data remains after unmarshal")
	}
	if !reflect.DeepEqual(&empty, &full) {
		t.Errorf("bad result. expected:")
		t.Errorf("%+v\n", empty)
		t.Errorf("got:")
		t.Errorf("%+v\n", full)
	}
}

func TestDirBlockMarshal(t *testing.T) {
	data, err := dirBlock1.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var new DirBlock
	remaining, err := new.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("data remains after unmarshal")
	}
	if !reflect.DeepEqual(&dirBlock1, &new) {
		t.Errorf("bad result. expected:")
		t.Errorf("%+v\n", &dirBlock1)
		t.Errorf("got:")
		t.Errorf("%+v\n", &new)
	}
}

func TestDirEntMarshalAppendNoMalloc(t *testing.T) {
	// Marshal to see what length we need.
	data, err := dirEnt.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Toss old data but keep length.
	data = make([]byte, len(data))
	p := &data[0]
	data, err = dirEnt.MarshalAppend(data[:0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if p != &data[0] {
		t.Fatalf("MarshalAppend allocated")
	}
}

func TestDirEntUnmarshalNoPanic(t *testing.T) {
	data, err := dirEnt.Unmarshal([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x2})
	if err != ErrTooShort {
		t.Fatal(err)
	}
	if data != nil {
		t.Fatalf("Expected no data, got %d bytes", len(data))
	}
}
