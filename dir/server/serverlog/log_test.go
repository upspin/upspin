// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverlog

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var (
	user  upspin.UserName = "foo@bar.com"
	entry                 = Entry{
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
	var newEntry Entry
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
	dir, cleanup := setup(t, "Concurrent")
	defer cleanup()

	logRW, _, err := New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer logRW.Close()
	abort := make(chan struct{})
	aborted := func() bool {
		select {
		case <-abort:
			return true
		default:
			return false
		}
	}
	write := func() error {
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
					return err
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
				return nil
			}
			err := logRW.Append(&e)
			if err != nil {
				return err
			}
		}
		return nil
	}
	read := func() error {
		logRO, err := logRW.NewReader()
		if err != nil {
			return err
		}
		defer logRO.Close()
		var offset int64
		for i := 0; i < numEntries*numWriters; i++ {
			if aborted() {
				return nil
			}
			_, next, err := logRO.ReadAt(offset)
			if err != nil {
				return err
			}
			if offset == next {
				i--
				continue
			}
			offset = next
		}
		return nil
	}

	errc := make(chan error, numWriters+numReaders)
	for i := 0; i < numWriters; i++ {
		go func() { errc <- write() }()
	}
	for i := 0; i < numReaders; i++ {
		go func() { errc <- read() }()
	}
	for i := 0; i < numWriters+numReaders; i++ {
		if err := <-errc; err != nil {
			close(abort)
			t.Fatal(err)
		}
	}
}

func TestAppendRead(t *testing.T) {
	const minEntrySize = 30 // Just a hint so we can assert offsets.
	dir, cleanup := setup(t, "AppendRead")
	defer cleanup()

	logger, _, err := New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := logger.User(), user; got != want {
		t.Errorf("logger.User = %q, want = %q", got, want)
	}

	for i := 0; i < 10; i++ {
		le := newEntry(upspin.PathName(fmt.Sprintf("foo@bar.com/hello%d", i)), i+1)
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
	lrd, err := logger.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	offset := int64(0)
	for i := 0; i < 11; i++ { // Tries to go past EOF.
		entry, next, err := lrd.ReadAt(offset)
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
}

func TestOldStyleLogs(t *testing.T) {
	dir, cleanup := setup(t, "OldStyleLogs")
	defer cleanup()

	err := os.Mkdir(logSubDir(user, dir), 0700)
	if err != nil {
		t.Fatal(err)
	}
	// Create an existing old-style log.
	f, err := os.Create(filepath.Join(dir, oldStyleLogFilePrefix+string(user)))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Makes a hard link to the existing old style.
	l, _, err := New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()

	// Open it again. No errors.
	l, _, err = New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestReadRotatedLog(t *testing.T) {
	dir, cleanup := setup(t, "ReadRotatedLog")
	defer cleanup()

	// Simulate a rotated log exists. NewLog will open the rotated one and
	// read from it.

	err := os.Mkdir(logSubDir(user, dir), 0700)
	if err != nil {
		t.Fatal(err)
	}

	// Create a few rotated logs.
	f, err := os.Create(logFile(user, 345678, dir))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = os.Create(logFile(user, 555111, dir))
	if err != nil {
		t.Fatal(err)
	}

	// Open Logs for user.
	l, _, err := New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	err = l.Append(&entry)
	if err != nil {
		t.Fatal(err)
	}

	// Verify we appended some bytes to the right place. The exact size is
	// not important, but something greater than zero.
	if fi, err := f.Stat(); err == nil && fi.Size() < 30 {
		t.Fatalf("Append did not write to the rotated file; read %d bytes", fi.Size())
	} else if err != nil {
		t.Fatal(err)
	}
	l.Close()
	f.Close()

	// Create one more rotated log so we can test reading from the middle.
	f, err = os.Create(logFile(user, 777222, dir))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Open Logs again and get a reader reading from 345678.
	l, _, err = New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := l.NewReader()
	if err != nil {
		t.Fatal(err)
	}

	le, _, err := rd.ReadAt(555111)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&le, &entry) {
		t.Fatalf("Expected\n%+v\nGot:\n%+v", entry, le)
	}
}

func TestRotateLogAndTruncate(t *testing.T) {
	const user = "bob@example.com"
	dir, err := ioutil.TempDir("", "TestRotateLog")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	prevMaxLogSize := MaxLogSize
	MaxLogSize = 100
	defer func() {
		MaxLogSize = prevMaxLogSize
	}()

	log, _, err := New(user, dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		entry := newEntry(upspin.PathName(user+"/testing-testing"), 1)
		err := log.Append(entry)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Verify we have logs of roughly at the expected offsets.
	offsets := logOffsetsFor(logSubDir(user, dir))
	expectedOffs := []int64{464, 348, 232, 116, 0}
	if got, want := len(offsets), len(expectedOffs); got != want {
		t.Fatalf("Expected %d offsets, got %d", want, got)
	}
	if !reflect.DeepEqual(offsets, expectedOffs) {
		t.Fatalf("Expected\n%+v\nGot:\n%+v", expectedOffs, offsets)
	}

	// Check that truncation works.
	err = log.Truncate(300)
	if err != nil {
		t.Fatal(err)
	}
	offsets = logOffsetsFor(logSubDir(user, dir))
	expectedOffs = []int64{232, 116, 0}
	if got, want := len(offsets), len(expectedOffs); got != want {
		t.Fatalf("Expected %d offsets, got %d", want, got)
	}
	if !reflect.DeepEqual(offsets, expectedOffs) {
		t.Fatalf("Expected\n%+v\nGot:\n%+v", expectedOffs, offsets)
	}

	r, err := log.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	// Read at a valid offset and verify there's a next record.
	_, next, err := r.ReadAt(232)
	if err != nil {
		t.Fatal(err)
	}
	if next <= 232 {
		t.Fatalf("Expected next > 232, got %d", next)
	}
}

func TestIndex(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestAppendRead")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	_, logIndex, err := New("foo@bar.com", dir)
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
	dir, cleanup := setup(t, "ListUsers")
	defer cleanup()

	// Create a few test users.
	for _, u := range []upspin.UserName{
		"morihei@ueshiba.jp",
		"kishomaru@ueshiba.jp",
		"moriteru@ueshiba.jp",
		"shiohira@shihan.com",
		"jose+photos@ortega.com",
		"morihei+snapshot@ueshiba.jp",
	} {
		_, _, err := New(u, dir)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Glob for snapshot users only.
	users, err := ListUsersWithSuffix("snapshot", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUsers(t, users, []upspin.UserName{"morihei+snapshot@ueshiba.jp"}) {
		t.Fatal("users don't match")
	}
	// Glob for .jp users only.
	users, err = userGlob("*.jp", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUsers(t, users, []upspin.UserName{
		"morihei+snapshot@ueshiba.jp",
		"kishomaru@ueshiba.jp",
		"moriteru@ueshiba.jp",
		"morihei@ueshiba.jp",
	}) {
		t.Fatal("users don't match")
	}
	// Glob for users with suffix only.
	users, err = ListUsersWithSuffix("*", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUsers(t, users, []upspin.UserName{
		"morihei+snapshot@ueshiba.jp",
		"jose+photos@ortega.com",
	}) {
		t.Fatal("users don't match")
	}
	// Get all users.
	users, err = ListUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUsers(t, users, []upspin.UserName{
		"morihei@ueshiba.jp",
		"kishomaru@ueshiba.jp",
		"moriteru@ueshiba.jp",
		"shiohira@shihan.com",
		"jose+photos@ortega.com",
		"morihei+snapshot@ueshiba.jp",
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

func newEntry(path upspin.PathName, seq int) *Entry {
	var op Operation
	if seq%2 == 0 {
		op = Delete
	} else {
		op = Put
	}
	return &Entry{
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

// setup creates a testing directory and returns its name and a cleanup
// function.
func setup(t *testing.T, testName string) (string, func()) {
	dir, err := ioutil.TempDir("", testName)
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// For sorting a slice of upspin.UserName.
type userNameSlice []upspin.UserName

func (p userNameSlice) Len() int           { return len(p) }
func (p userNameSlice) Less(i, j int) bool { return p[i] < p[j] }
func (p userNameSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
