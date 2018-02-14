// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverlog

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/storagetest"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

var (
	userName upspin.UserName = "foo@bar.com"
	entry                    = Entry{
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
	count, err := newEntry.unmarshal(bytes.NewReader(buf), make([]byte, 1024), 0)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(buf) {
		t.Fatalf("got %d bytes; want %d", count, len(buf))
	}

	if !reflect.DeepEqual(&entry, &newEntry) {
		t.Errorf("newEntry = %v, want = %v", newEntry, entry)
	}
}

func BenchmarkReadAt(b *testing.B) {
	dir, cleanup := setup(b, "BenchmarkReadAt")
	defer cleanup()

	user, err := Open(userName, dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	err = user.Append(newEntry(upspin.PathName("foo@bar.com/hello"), 1))
	if err != nil {
		b.Fatal(err)
	}
	lrd, err := user.NewReader()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := lrd.ReadAt(0)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestCountReadAtMallocs(t *testing.T) {
	dir, cleanup := setup(t, "AppendRead")
	defer cleanup()

	user, err := Open(userName, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = user.Append(newEntry(upspin.PathName("foo@bar.com/hello"), 1))
	if err != nil {
		t.Fatal(err)
	}
	lrd, err := user.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	fn := func() {
		_, _, err := lrd.ReadAt(0)
		if err != nil {
			t.Fatal(err)
		}
	}
	mallocs := testing.AllocsPerRun(100, fn)
	if got, want := mallocs, 3.0; got != want {
		t.Errorf("got %v allocs, want <=%v", got, want)
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

	user, err := Open(userName, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer user.Close()
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
			e.Entry.Sequence = rand.Int63n(100)
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
			err := user.Append(&e)
			if err != nil {
				return err
			}
		}
		return nil
	}
	read := func() error {
		logRO, err := user.NewReader()
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

	user, err := Open(userName, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		le := newEntry(upspin.PathName(fmt.Sprintf("foo@bar.com/hello%d", i)), i+1)
		err := user.Append(le)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Offset must have moved.
	if got, wantAtLeast := user.AppendOffset(), int64(minEntrySize*10); got < wantAtLeast {
		t.Errorf("LastOffset = %d, want > %d", got, wantAtLeast)
	}
	// Read LogEntries back.
	lrd, err := user.NewReader()
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

	if want := user.AppendOffset(); offset != want {
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

	const name = "bob@example.com"

	// Create an existing old-style log.
	oldLog := filepath.Join(dir, oldStyleLogFilePrefix+string(name))
	err := ioutil.WriteFile(oldLog, []byte{}, 0600)
	if err != nil {
		t.Fatal(err)
	}

	// Open moves the old file to its new location.
	u, err := Open(name, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	u.Close()

	// Check that the old log has been removed.
	_, err = os.Stat(oldLog)
	if !os.IsNotExist(err) {
		t.Fatalf("expected not exist error for %q, got %v", oldLog, err)
	}

	// Open it again. Should work fine.
	u, err = Open(name, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	u.Close()

	// Re-create the old log.
	err = ioutil.WriteFile(oldLog, []byte{}, 0600)
	if err != nil {
		t.Fatal(err)
	}

	// Open it again.
	u, err = Open(name, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	u.Close()

	// Check that the old log was simply purged.
	_, err = os.Stat(oldLog)
	if !os.IsNotExist(err) {
		t.Fatalf("expected not exist error, got %v", err)
	}
}

func TestWhichLogFile(t *testing.T) {
	const (
		numLogs = 10
		size    = 100
	)

	// Fake a list of files and offsets.
	var user User

	for i := 0; i < numLogs; i++ {
		lf := &logFile{
			index:   len(user.files),
			version: version,
			offset:  int64(i * size),
		}
		user.files = append(user.files, lf)
	}

	for offset := 0; offset < numLogs*size; offset++ {
		f := user.whichLogFile(int64(offset))
		if f.index != offset/size { // Integer division gets the boundary right.
			t.Fatalf("got log %d; expected %d", f.index, offset/size)
		}
	}

	// Try off the end. Shouldn't happen but get it right
	f := user.whichLogFile(numLogs*size + 100)
	if f.index != len(user.files)-1 {
		t.Fatal("off end did not get last entry")
	}
}

func TestReadRotatedLog(t *testing.T) {
	dir, cleanup := setup(t, "ReadRotatedLog")
	defer cleanup()

	user := &User{
		name:      "bob@example.com",
		directory: dir,
	}

	// Simulate a rotated log. NewLog will open the rotated one and
	// read from it.

	err := os.Mkdir(user.logSubDir(), 0700)
	if err != nil {
		t.Fatal(err)
	}

	// Create a few rotated logs.
	f, err := os.Create(user.logFileName(345678, version))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = os.Create(user.logFileName(555111, version))
	if err != nil {
		t.Fatal(err)
	}

	// Open logs for user.
	user2, err := Open(user.name, user.directory, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = user2.Append(&entry)
	if err != nil {
		t.Fatal(err)
	}
	user2.Close()

	// Verify we appended some bytes to the right place. The exact size is
	// not important, but something greater than zero.
	if fi, err := f.Stat(); err == nil && fi.Size() < 30 {
		t.Fatalf("Append did not write to the rotated file; read %d bytes", fi.Size())
	} else if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Create one more rotated log so we can test reading from the middle.
	f, err = os.Create(user.logFileName(777222, version))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Open Logs again and get a reader reading from 345678.
	user3, err := Open(user.name, user.directory, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer user3.Close()
	rd, err := user3.NewReader()
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

	user, err := Open("bob@example.com", dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		entry := newEntry(upspin.PathName(user.name+"/testing-testing"), 1)
		err := user.Append(entry)
		if err != nil {
			t.Fatal(err)
		}
	}
	logOffsets := func(u *User) []int64 {
		var offs []int64
		for _, f := range u.files {
			offs = append(offs, f.offset)
		}
		return offs
	}
	// Verify we have logs of roughly at the expected offsets.
	offsets := logOffsets(user)
	expectedOffs := []int64{0, 116, 232, 348, 464}
	if got, want := len(offsets), len(expectedOffs); got != want {
		t.Fatalf("Expected %d offsets, got %d", want, got)
	}
	if !reflect.DeepEqual(offsets, expectedOffs) {
		t.Fatalf("Expected\n%+v\nGot:\n%+v", expectedOffs, offsets)
	}

	// Check that truncation works.
	err = user.Truncate(300)
	if err != nil {
		t.Fatal(err)
	}
	offsets = logOffsets(user)
	expectedOffs = []int64{0, 116, 232}
	if got, want := len(offsets), len(expectedOffs); got != want {
		t.Fatalf("Expected %d offsets, got %d", want, got)
	}
	if !reflect.DeepEqual(offsets, expectedOffs) {
		t.Fatalf("Expected\n%+v\nGot:\n%+v", expectedOffs, offsets)
	}

	r, err := user.NewReader()
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

	store := notifyingStorage{
		storagetest.Memory(),
		make(chan bool, 1),
	}
	fac, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "test"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := Open("reallylongusernamefoo@bar.com", dir, fac, store)
	if err != nil {
		t.Fatal(err)
	}

	// Read before write.
	_, err = user.Root()
	expectedErr := errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want = %s", err, expectedErr)
	}

	root := upspin.DirEntry{
		Name:       "reallylongusernamefoo@bar.com/dir/file.txt",
		SignedName: "reallylongusernamefoo@bar.com/dir/file.txt",
		Writer:     "writer@bar.com",
		Packing:    upspin.PlainPack,
		Packdata:   []byte("some pack data"),
		Attr:       upspin.AttrLink,
		Sequence:   17,
		Time:       1234567890,
	}
	err = user.SaveRoot(&root)
	if err != nil {
		t.Fatal(err)
	}
	recoveredRoot, err := user.Root()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recoveredRoot, &root) {
		t.Errorf("recoveredRoot = %v, want = %v", recoveredRoot, root)
	}

	// Check that root.ref is stable.
	rootRef, err := hashRoot("tree.root.reallylongusernamefoo@bar.com", fac)
	if err != nil {
		t.Fatal(err)
	}
	if rootRef != user.root.ref {
		t.Errorf("rootRef = %v, want = %v", rootRef, user.root.ref)
	}

	// Check that the root was put to the storage backend.
	<-store.onPut
	b, err := store.Storage.Download(user.root.ref)
	if err != nil {
		t.Fatal(err)
	}
	var storedRoot upspin.DirEntry
	_, err = storedRoot.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedRoot, root) {
		t.Errorf("storedRoot = %v, want = %v", storedRoot, root)
	}

	// Save and read offset
	offset := int64(123456)
	err = user.SaveOffset(offset)
	if err != nil {
		t.Fatal(err)
	}
	recoveredOffset, err := user.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if recoveredOffset != offset {
		t.Errorf("recoveredOffset = %d, want = %d", recoveredOffset, offset)
	}

	// Clone the log index and ensure it's read-only.
	clone, err := user.checkpoint.readOnlyClone()
	if err != nil {
		t.Fatal(err)
	}
	offset, err = clone.readOffset()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := offset, recoveredOffset; got != want {
		t.Errorf("LastOffset = %d, want = %d", got, want)
	}
	// Now write something and get an error.
	err = clone.saveOffset(999999)
	expectedErr = errors.E(errors.IO)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
	}
}

type notifyingStorage struct {
	storage.Storage
	onPut chan bool
}

func (s notifyingStorage) Put(ref string, b []byte) error {
	err := s.Storage.Put(ref, b)
	s.onPut <- true
	return err
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
		user, err := Open(u, dir, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		user.Close()
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

func TestAddOffSeq(t *testing.T) {
	// Generate a random ordering and make sure it comes out sorted.
	var u User // Zero value will do.
	perm := rand.Perm(100)
	for _, o := range perm {
		o64 := int64(o)
		u.addOffSeq(o64, o64) // offset == sequence is fine for this purpose.
	}
	if len(u.offSeqs) != len(perm) {
		t.Fatalf("got %d elements, expected %d", len(u.offSeqs), len(perm))
	}
	for i, offseq := range u.offSeqs {
		if offseq.offset != int64(i) {
			t.Fatalf("%d: got %d; expected %d", i, offseq.offset, i)
		}
	}
}

func TestOffsetOf(t *testing.T) {
	const numEntries = 100
	dir, cleanup := setup(t, "OffsetOf")
	defer cleanup()

	user, err := Open(userName, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer user.Close()

	// Create our own definition of the mapping.
	sequenceAtOffset := make(map[int64]int64)
	offsetAtSequence := make(map[int64]int64)
	seq := int64(upspin.SeqBase)
	for i := 0; i < numEntries; i, seq = i+1, seq+1 {
		e := entry
		e.Entry.Sequence = seq
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
				log.Fatal(err)
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
		offset := user.AppendOffset()
		sequenceAtOffset[offset] = seq
		offsetAtSequence[seq] = offset
		err := user.Append(&e)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Verify our data.
	reader, err := user.NewReader()
	if err != nil {
		log.Fatal(err)
	}
	defer reader.Close()
	var offset int64
	for i := 0; i < numEntries; i++ {
		le, next, err := reader.ReadAt(offset)
		if err != nil {
			log.Fatal(err)
		}
		if offset == next {
			i--
			continue
		}
		if seq := sequenceAtOffset[offset]; seq != le.Entry.Sequence {
			log.Fatalf("%d: bad seq; got %d, expected %d", i, le.Entry.Sequence, seq)
		}
		if off := offsetAtSequence[le.Entry.Sequence]; off != offset {
			log.Fatalf("%d: bad offset; got %d, expected %d", i, off, offset)
		}
		offset = next
	}

	// Now ask the system. Iterating over the map asks in random order,
	// which is good.
	for seq, offset := range offsetAtSequence {
		got := user.OffsetOf(seq)
		if got != offset {
			t.Errorf("OffsetOf(%d) = %d; want %d", seq, got, offset)
		}
	}
}

func TestVersion0Logs(t *testing.T) {
	// Copy logs to temporary so we don't overwrite any.
	dir, err := ioutil.TempDir("", "TestVersion0Logs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	startTime := upspin.Now()

	err = os.MkdirAll(filepath.Join(dir, "d.tree.log.user@example.com"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk("testdata", func(path string, info os.FileInfo, _ error) error {
		if info.IsDir() {
			return nil
		}
		data, err := ioutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, path[len("testdata/version0/"):])
		err = ioutil.WriteFile(tmp, data, 0600)
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Now we can Open the user's logs in the temporary location.
	user, err := Open("user@example.com", dir, nil, nil)
	defer os.Remove("testdata/version0/d.tree.log.user@example.com/2738.1")
	if err != nil {
		t.Fatal(err)
	}
	r, err := user.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	// This is the series of events in testdata/version0log.
	sequence := []int64{0, 1, 1, 1, 1, 1, 2}
	name := []upspin.PathName{"dir", "dir/file", "file", "file", "dir/file", "dir/file", "dir/file"}
	op := []Operation{Put, Put, Put, Delete, Delete, Put, Put}
	offset := int64(0)
	for i := 0; i < 100; i++ {
		entry, next, err := r.ReadAt(offset)
		if err != nil {
			log.Fatal(err)
		}
		if offset == next {
			break
		}
		if r.file.version != 0 {
			t.Fatalf("log is version %d; expected version 0", r.file.version)
		}
		if entry.Entry.Sequence != sequence[i] {
			t.Fatalf("%d: got sequence %d; expected %d", i, entry.Entry.Sequence, sequence[i])
		}
		path := "user@example.com/" + name[i]
		if entry.Entry.Name != path {
			t.Fatalf("%d: got name %q; expected %q", i, entry.Entry.Name, path)
		}
		if entry.Op != op[i] {
			t.Fatalf("%d: got op %d; expected %d", i, entry.Op, op[i])
		}
		t.Log(entry.Entry.Name, entry.Op)
		offset = next
	}
	// We only write to files with the current version.
	if user.writer.file.version != version {
		t.Fatalf("writer is to file version %d; expected %d", user.writer.file.version, version)
	}
	if user.writer.file.offset != offset {
		t.Fatalf("writer at offset %d; expected %d", user.writer.file.offset, offset)
	}

	// Verify the transition time. The files are golden so it's a fixed instant.
	got := user.V1Transition()
	if got < startTime {
		t.Fatalf("got transition time %s; want a time after %s", got, startTime)
	}

	// We now have an empty version 1 log. Close and reopen.
	// This was a bug.
	err = user.Close()
	if err != nil {
		t.Fatal(err)
	}
	user, err = Open("user@example.com", dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got = user.V1Transition()
	if got < startTime {
		t.Fatalf("got transition time %s; want a time after %s", got, startTime)
	}
	user.Close()
	err = user.Close()
	if err != nil {
		t.Error(err)
	}
}

func TestReOpen(t *testing.T) {
	dir, err := ioutil.TempDir("", "ReOpen")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const first, last = 42, 420

	oldMax := MaxLogSize
	MaxLogSize = 1024
	defer func() {
		MaxLogSize = oldMax
	}()

	user, err := Open("user@example.com", dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(first); i < last; i++ {
		err = user.Append(&Entry{
			Op: Put,
			Entry: upspin.DirEntry{
				Name:     "user@example.com/foo",
				Sequence: i,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err = user.Close()
	if err != nil {
		t.Fatal(err)
	}

	user, err = Open("user@example.com", dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := user.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(first); i < last; i++ {
		entry, _, err := r.ReadAt(user.OffsetOf(i))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := entry.Entry.Sequence, i; got != want {
			t.Errorf("got sequence %d, want %d", got, want)
		}
	}
	err = user.Close()
	if err != nil {
		t.Fatal(err)
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
func setup(t testing.TB, testName string) (string, func()) {
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
