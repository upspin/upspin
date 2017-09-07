// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"fmt"
	"reflect"
	"strings"
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
	// These are nonsense entries, with many empty fields, but it's just a test.
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
	_, err := dirEnt.Unmarshal([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x2})
	if err != ErrTooShort {
		t.Fatal(err)
	}
}

// Test inputs that caused crashes with go-fuzz.
func TestDirEntryUnmarshalCrashers(t *testing.T) {
	inputs := []string{
		"*0000000000000000000" +
			"0000\x00\b0000\x00\x1200000000" +
			"0\x13",
		"*u@foo.co\xbdm/a/direct" +
			"ory\x14\x80�\x04��",
		"\x0000\x80�\x04",
		"*0000000000000000000" +
			"0000\x90�@",
		"*@foo.com/a/director" +
			"y\x14\x80\x89\x0e\xfe�\x16~��$\xef\xbf" +
			"\xbd\x12\x02\x18foo.com:1234\fCin" +
			"{er\x00\x80\x80\xae\xbfｿｿｿｿ" +
			"\xef",
		"*0000000000000000000" +
			"000000\x18000000000000\x06" +
			"000���0",
		"\x000\xbd\xbd\xbfｿ\u07fd\xbf0",
	}
	for _, s := range inputs {
		var de DirEntry
		de.Unmarshal([]byte(s))
	}
}

// Tests of the buffer overflow code. These know about the structure of
// the code, but otherwise we'd be playing with 2GB buffers.

func TestMarshalBigDirBlock(t *testing.T) {
	defer func() { maxInt32 = 1<<31 - 1 }()
	maxInt32 = 1<<15 - 1 // Has been tested on a 64-bit machine at 1<<28-1.
	d := DirBlock{
		Location: Location{
			Endpoint: Endpoint{
				Transport: Remote,
				NetAddr:   "upspin.io",
			},
			Reference: "foo",
		},
		Offset:   0,
		Size:     0,
		Packdata: []byte{},
	}
	// Should succeed.
	_, err := d.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal failed: ", err)
	}
	// Big Size should fail.
	d.Size = MaxBlockSize + 1
	_, err = d.MarshalAppend(nil)
	if err != ErrTooLarge {
		t.Error("Marshal big Size should fail with ErrTooLarge; got: ", err)
	}
	d.Size = 0
	// Big Packdata should fail.
	big := make([]byte, maxInt32+1)
	d.Packdata = big
	_, err = d.MarshalAppend(nil)
	if err != ErrTooLarge {
		t.Error("Marshal big Packdata should fail with ErrTooLarge; got: ", err)
	}
	// Every field OK but doesn't quite fit.
	d.Packdata = big[:len(big)-10]
	_, err = d.MarshalAppend(nil)
	if err != ErrTooLarge {
		t.Error("Marshal big Packdata should fail with ErrTooLarge; got: ", err)
	}
	// A little smaller should be OK.
	// Start with enough headroom to discover the boundary.
	d.Packdata = big[:len(big)-64]
	b, err := d.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal just right Packdata should not fail; got: ", err)
	}
	startLen := uint64(len(b) - len(d.Packdata)) // It's 20, but let's compute it.
	// Now we can get just to the edge.
	d.Packdata = big[:maxInt32-startLen]
	_, err = d.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal just right Packdata should not fail; got: ", err)
	}
	// One more byte should fail.
	d.Packdata = big[:maxInt32-startLen+1]
	_, err = d.MarshalAppend(nil)
	if err != ErrTooLarge {
		t.Error("Marshal just too big Packdata should fail with ErrTooLarge; got: ", err)
	}
}

func TestMarshalBigDirEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("Allocates too much for a short run.")
	}
	defer func() { maxInt32 = 1<<31 - 1 }()
	maxInt32 = 1<<15 - 1 // Has been tested on a 64-bit machine at 1<<28-1.
	de := dirEnt

	// Should succeed.
	_, err := de.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal failed: ", err)
	}
	// Too many blocks should fail.
	manyBlocks := make([]DirBlock, maxInt32+1)
	de.Blocks = manyBlocks
	_, err = de.MarshalAppend(nil)
	if err != ErrTooLarge {
		t.Error("Marshal too many blocks should fail with ErrTooLarge; got: ", err)
	}
	// Too much data should fail. Approach the actual size in steps.
	for size := 1000; ; size *= 2 {
		de.Blocks = manyBlocks[:size] // Might as well re-use it.
		for i := range de.Blocks {
			de.Blocks[i] = dirBlock1
		}
		b, err := de.MarshalAppend(nil)
		if err == nil {
			if uint64(len(b)) > maxInt32 {
				t.Fatalf("should have failed at size %d", len(b))
			}
			continue
		}
		if err != ErrTooLarge {
			t.Fatal("unexpected error", err)
		}
		break
	}
}

func TestUnmarshalBigDirBlock(t *testing.T) {
	d := DirBlock{
		Location: Location{
			Endpoint: Endpoint{
				Transport: Remote,
				NetAddr:   "upspin.io",
			},
			Reference: "foo",
		},
		Offset:   0,
		Size:     0,
		Packdata: make([]byte, 1<<15), // Small enough now, not later.
	}
	// Should succeed.
	data, err := d.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal failed: ", err)
	}
	var db DirBlock
	b, err := db.Unmarshal(data)
	if err != nil {
		t.Error("Unmarshal failed: ", err)
	}
	if len(b) != 0 {
		t.Error("leftover data after Unmarshal")
	}
	if !reflect.DeepEqual(db, d) {
		t.Error("data incorrect after Unmarshal")
		t.Errorf("input: %#v", d)
		t.Errorf("output: %#v", db)
	}
	// We now know that data correctly unmarshals.
	// Change maxInt32 and we should fail.
	defer func() { maxInt32 = 1<<31 - 1 }()
	maxInt32 = 1<<15 - 1
	_, err = d.Unmarshal(data)
	if err != ErrTooLarge {
		t.Errorf("Unmarshal got %v; want ErrTooLarge", err)
	}
}

func TestUnmarshalBigDirEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("Allocates too much for a short run.")
	}
	d := dirEnt
	// We probed Packdata in TestUnmarshalBigDirBlock
	// so we do something else here.
	d.Link = PathName(strings.Repeat("x", 1<<15))

	// Should succeed for now.
	data, err := d.MarshalAppend(nil)
	if err != nil {
		t.Error("Marshal failed: ", err)
	}
	var de DirEntry
	b, err := de.Unmarshal(data)
	if err != nil {
		t.Error("Unmarshal failed: ", err)
	}
	if len(b) != 0 {
		t.Error("leftover data after Unmarshal")
	}
	if !reflect.DeepEqual(de, d) {
		t.Error("data incorrect after Unmarshal")
		t.Errorf("input: %#v", d)
		t.Errorf("output: %#v", de)
	}

	// We now know that data correctly unmarshals.
	// Change maxInt32 and we should fail.
	defer func() { maxInt32 = 1<<31 - 1 }()
	maxInt32 = 1<<15 - 1 // Has been tested on a 64-bit machine at 1<<28-1.
	_, err = d.Unmarshal(data)
	if err != ErrTooLarge {
		t.Errorf("Unmarshal got %v; want ErrTooLarge", err)
	}
}
