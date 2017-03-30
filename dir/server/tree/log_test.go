// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var (
	user  upspin.UserName = "foo@bar.com"
	entry                 = LogEntry{
		Op: Delete,
		Entry: upspin.DirEntry{
			Name:       "foo@bar.com/dir/file.txt",
			SignedName: "foo@bar.com/dir/file.txt",
			Writer:     "writer@bar.com",
			Packing:    upspin.PlainPack,
			Packdata:   []byte("some pack data"),
			Attr:       upspin.AttrLink,
			Sequence:   17,
			Time:       1234567890,
		},
	}
)

func TestMarshalUnmarshal(t *testing.T) {
	buf, err := entry.marshal()
	if err != nil {
		t.Fatal(err)
	}
	var newEntry LogEntry
	r := newChecker(bufio.NewReader(bytes.NewReader(buf)))
	err = newEntry.unmarshal(r)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(&entry, &newEntry) {
		t.Errorf("newEntry = %v, want = %v", newEntry, entry)
	}
}

func TestConcurrent(t *testing.T) {
	const (
		numWriters = 3
		numReaders = 2
		numEntries = 100
	)
	if testing.Short() {
		// To run faster, run the log on a ram disk:
		// mkdir /dev/shm/test
		// env TMPDIR=/dev/shm/test go test -run=Concurrent
		t.Skip("Concurrent test takes too long")
	}
	dir, err := ioutil.TempDir("", "TestConcurrent")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	logRW, _, err := NewLogs(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer logRW.Close()
	var done sync.WaitGroup
	start := make(chan struct{})
	aborting := int32(0) // if positive, indicates a fatal and all must quit.
	abort := func() { atomic.StoreInt32(&aborting, 1) }
	aborted := func() bool { return atomic.LoadInt32(&aborting) == 1 }
	write := func() {
		defer done.Done()
		<-start
		for i := 0; i < numEntries; i++ {
			e := entry
			e.Entry.Sequence = upspin.NewSequence()
			e.Entry.Time = upspin.Now()
			if rand.Intn(10) == 0 {
				e.Entry.SignedName = "bar@foo.com/otherfile"
			}
			if rand.Intn(10) == 0 {
				e.Entry.Link = "hello@example.com/subdir/file"
			}
			if rand.Intn(5) == 0 {
				e.Entry.Writer = "meh@yo.com"
			}
			numBlocks := rand.Intn(20)
			var offs int64
			for b := 0; b < numBlocks; b++ {
				packSize := rand.Intn(3000)
				packdata := make([]byte, packSize)
				_, err := rand.Read(packdata)
				if err != nil {
					abort()
					t.Fatal(err)
				}
				size := rand.Int63n(1000)
				block := upspin.DirBlock{
					Offset:   offs,
					Size:     size,
					Packdata: packdata,
				}
				offs += size
				e.Entry.Blocks = append(e.Entry.Blocks, block)
			}
			if aborted() {
				return
			}
			err := logRW.Append(&e)
			if err != nil {
				abort()
				t.Fatal(err)
			}
		}
	}
	read := func() {
		defer done.Done()
		logRO, err := logRW.Clone()
		if err != nil {
			t.Fatal(err)
		}
		defer logRO.Close()
		<-start
		var offset int64
		for i := 0; i < numEntries*numWriters; i++ {
			if aborted() {
				return
			}
			_, next, err := logRO.ReadAt(offset)
			if err != nil {
				abort()
				t.Fatal(err)
			}
			if offset == next {
				i--
				continue
			}
			offset = next
		}
	}

	done.Add(numWriters + numReaders)
	for i := 0; i < numWriters; i++ {
		go write()
	}
	for i := 0; i < numReaders; i++ {
		go read()
	}
	close(start)
	done.Wait()
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
		le := newLogEntry(upspin.PathName(fmt.Sprintf("foo@bar.com/hello%d", i)), i+1)
		err := logger.Append(le)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Offset must have moved.
	if got, wantAtLeast := logger.LastOffset(), int64(minEntrySize*10); got < wantAtLeast {
		t.Errorf("LastOffset = %d, want > %d", got, wantAtLeast)
	}
	// Read LogEntries back.
	var entries []LogEntry
	offset := int64(0)
	for i := 0; i < 11; i++ { // Tries to go past EOF.
		entry, next, err := logger.ReadAt(offset)
		if err != nil {
			t.Fatal(err)
		}
		if next == offset {
			break
		}
		offset = next
		entries = append(entries, entry)
	}

	if want := logger.LastOffset(); offset != want {
		t.Errorf("nextOffset = %d, want = %d", offset, want)
	}
	// Spot-check some entries.
	if got, want := string(entries[6].Entry.Name), "foo@bar.com/hello6"; got != want {
		t.Errorf("entries[6].Entry.Name = %q, want = %q", got, want)
	}
	if got, want := entries[6].Op, Put; got != want {
		t.Errorf("entries[6].Op = %v, want = %v", got, want)
	}
	if got, want := string(entries[9].Entry.Name), "foo@bar.com/hello9"; got != want {
		t.Errorf("entries[9].Entry.Name = %q, want = %q", got, want)
	}

	// Clone the log and ensure it's read-only.
	clone, err := logger.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := clone.LastOffset(), logger.LastOffset(); got != want {
		t.Errorf("LastOffset = %d, want = %d", got, want)
	}
	entry, offset, err = clone.ReadAt(0)
	if err != nil {
		t.Fatal(err)
	}
	err = clone.Append(newLogEntry(upspin.PathName("foo@bar.com/yabbadabadoo"), 17))
	expectedErr := errors.E(errors.IO)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
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
		t.Errorf("err = %s, want = %s", err, expectedErr)
	}

	root := upspin.DirEntry{
		Name:       "foo@bar.com/dir/file.txt",
		SignedName: "foo@bar.com/dir/file.txt",
		Writer:     "writer@bar.com",
		Packing:    upspin.PlainPack,
		Packdata:   []byte("some pack data"),
		Attr:       upspin.AttrLink,
		Sequence:   17,
		Time:       1234567890,
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

	// Clone the log index and ensure it's read-only.
	clone, err := logIndex.Clone()
	if err != nil {
		t.Fatal(err)
	}
	offset, err = clone.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := offset, recoveredOffset; got != want {
		t.Errorf("LastOffset = %d, want = %d", got, want)
	}
	// Now write something and get an error.
	err = clone.SaveOffset(999999)
	expectedErr = errors.E(errors.IO)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
	}
}

func TestListUsers(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestListUsers")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create a few test users.
	for _, u := range []upspin.UserName{
		"morihei@ueshiba.jp",
		"kishomaru@ueshiba.jp",
		"moriteru@ueshiba.jp",
		"shiohira@shihan.com",
		"jose+photos@ortega.com",
		"morihei+snapshot@ueshiba.jp",
	} {
		_, _, err := NewLogs(u, dir)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Glob for snapshot users only.
	users, err := ListUsers("*+snapshot@*", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUsers(t, users, []upspin.UserName{"morihei+snapshot@ueshiba.jp"}) {
		t.Fatal("users don't match")
	}
	// Glob for .jp users only.
	users, err = ListUsers("*.jp", dir)
	if !sameUsers(t, users, []upspin.UserName{
		"morihei+snapshot@ueshiba.jp",
		"kishomaru@ueshiba.jp",
		"moriteru@ueshiba.jp",
		"morihei@ueshiba.jp",
	}) {
		t.Fatal("users don't match")
	}
	// Glob for users with suffix only.
	users, err = ListUsers("*+*@*", dir)
	if !sameUsers(t, users, []upspin.UserName{
		"morihei+snapshot@ueshiba.jp",
		"jose+photos@ortega.com",
	}) {
		t.Fatal("users don't match")
	}
}

func TestChecksum(t *testing.T) {
	for i, tc := range []struct {
		buf    []byte
		chksum [4]byte
	}{
		{[]byte{}, [4]byte{0xde, 0xad, 0xbe, 0xef}},
		{[]byte{0xff}, [4]byte{0x21, 0xad, 0xbe, 0xef}},
		{[]byte{0xff, 0xff}, [4]byte{0x21, 0x52, 0xbe, 0xef}},
		{[]byte{0xff, 0xff, 0xff}, [4]byte{0x21, 0x52, 0x41, 0xef}},
		{[]byte{0, 0, 0, 0}, [4]byte{0xde, 0xad, 0xbe, 0xef}},
		{[]byte{0, 0, 0, 0, 1}, [4]byte{0xdf, 0xad, 0xbe, 0xef}},
		{[]byte{0, 0, 0, 0, 0xff}, [4]byte{0x21, 0xad, 0xbe, 0xef}},
		{[]byte{0xaa, 0x55, 0xff, 0x00, 0xab, 0x7f}, [4]byte{0xdf, 0x87, 0x41, 0xef}},
		{[]byte{1, 2, 3, 4, 5, 6, 7}, [4]byte{0xda, 0xa9, 0xba, 0xeb}},
	} {
		chksum := checksum(tc.buf)
		if tc.chksum != chksum {
			t.Errorf("%d: chksum = %x, want = %x", i, chksum, tc.chksum)
		}
	}
}

func newLogEntry(path upspin.PathName, seq int) *LogEntry {
	var op Operation
	if seq%2 == 0 {
		op = Delete
	} else {
		op = Put
	}
	return &LogEntry{
		Op: op,
		Entry: upspin.DirEntry{
			Name:       path,
			SignedName: path,
			Writer:     "foo@bar.com",
			Sequence:   int64(seq),
		},
	}
}

func sameUsers(t *testing.T, got, want []upspin.UserName) bool {
	if len(got) != len(want) {
		t.Errorf("got %d users, want %d", len(got), len(want))
		return false
	}
	sort.Sort(userNameSlice(got))
	sort.Sort(userNameSlice(want))
	for i, g := range got {
		if g != want[i] {
			t.Errorf("%d: got = %q, want = %q", i, g, want[i])
			return false
		}
	}
	return true
}

// For sorting a slice of upspin.UserName.
type userNameSlice []upspin.UserName

func (p userNameSlice) Len() int           { return len(p) }
func (p userNameSlice) Less(i, j int) bool { return p[i] < p[j] }
func (p userNameSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
