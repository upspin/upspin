// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var user upspin.UserName = "foo@bar.com"

func TestMarshalUnmarshal(t *testing.T) {
	entry := LogEntry{
		Op: Delete,
		Entry: upspin.DirEntry{
			Name:     "foo@bar.com/dir/file.txt",
			Writer:   "writer@bar.com",
			Packing:  upspin.PlainPack,
			Packdata: []byte("some pack data"),
			Attr:     upspin.AttrLink,
			Sequence: 17,
			Time:     1234567890,
		},
	}
	buf, err := entry.marshal()
	if err != nil {
		t.Fatal(err)
	}
	var newEntry LogEntry
	r := &countingByteReader{rd: bufio.NewReader(bytes.NewReader(buf))}
	err = newEntry.unmarshal(r)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(&entry, &newEntry) {
		t.Errorf("newEntry = %v, want = %v", newEntry, entry)
	}
}

func TestAppendRead(t *testing.T) {
	const minEntrySize = 30 // Just a hint so we can assert offsets.
	dir, err := ioutil.TempDir("", "TestAppendRead")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	logger, _, err := NewLogs(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := logger.User(), user; got != want {
		t.Errorf("logger.User = %q, want = %q", got, want)
	}

	for i := 0; i < 10; i++ {
		le := newLogEntry(upspin.PathName(fmt.Sprintf("foo@bar.com/hello%d", i)))
		err := logger.Append(le)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Offset must have moved.
	if got, wantAtLeast := logger.LastOffset(), int64(minEntrySize*10); got < wantAtLeast {
		t.Errorf("LastOffset = %d, want > %d", got, wantAtLeast)
	}
	// Read LogEntries back in two passes.
	entries, nextOffset, err := logger.ReadAt(6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(entries), 6; got != want {
		t.Fatalf("len(entries) = %d, want = %d", got, want)
	}
	if wantAtLeast := int64(minEntrySize * 6); nextOffset < wantAtLeast {
		t.Errorf("nextOffset = %d, want > %d", nextOffset, wantAtLeast)
	}
	// Read more. Attempt to go past the EOF
	entries, nextOffset, err = logger.ReadAt(32, nextOffset)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(entries), 4; got != want { // 4 remaining entries.
		t.Fatalf("len(entries) = %d, want = %d", got, want)
	}
	if want := logger.LastOffset(); nextOffset != want {
		t.Errorf("nextOffset = %d, want = %d", nextOffset, want)
	}
	// Spot-check some entries.
	if got, want := string(entries[0].Entry.Name), "foo@bar.com/hello6"; got != want {
		t.Errorf("entries[0].Entry.Name = %q, want = %q", got, want)
	}
	if got, want := entries[0].Op, Put; got != want {
		t.Errorf("entries[0].Op = %v, want = %v", got, want)
	}
	if got, want := string(entries[3].Entry.Name), "foo@bar.com/hello9"; got != want {
		t.Errorf("entries[3].Entry.Name = %q, want = %q", got, want)
	}
}

func TestLogIndex(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestAppendRead")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	_, logIndex, err := NewLogs("foo@bar.com", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := logIndex.User(), user; got != want {
		t.Errorf("logger.User = %q, want = %q", got, want)
	}

	// Read before write.
	_, err = logIndex.Root()
	expectedErr := errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("Expected error %s, got none", expectedErr)
	}

	root := upspin.DirEntry{
		Name:     "foo@bar.com/dir/file.txt",
		Writer:   "writer@bar.com",
		Packing:  upspin.PlainPack,
		Packdata: []byte("some pack data"),
		Attr:     upspin.AttrLink,
		Sequence: 17,
		Time:     1234567890,
	}
	err = logIndex.SaveRoot(&root)
	if err != nil {
		t.Fatal(err)
	}
	recoveredRoot, err := logIndex.Root()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recoveredRoot, &root) {
		t.Errorf("recoveredRoot = %v, want = %v", recoveredRoot, root)
	}

	// Save and read offset
	offset := int64(123456)
	err = logIndex.SaveOffset(offset)
	if err != nil {
		t.Fatal(err)
	}
	recoveredOffset, err := logIndex.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if recoveredOffset != offset {
		t.Errorf("recoveredOffset = %d, want = %d", recoveredOffset, offset)
	}
}

var seq int64

func newLogEntry(path upspin.PathName) *LogEntry {
	var op Operation
	seq++
	if seq%2 == 0 {
		op = Delete
	} else {
		op = Put
	}
	return &LogEntry{
		Op: op,
		Entry: upspin.DirEntry{
			Name:     path,
			Writer:   "foo@bar.com",
			Sequence: seq,
		},
	}
}
