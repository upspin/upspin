// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/key/inprocess"
	"upspin.io/log"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/store/inprocess"
)

const (
	userName   = "user@domain.com"
	serverName = "tree@server.com"
	isDir      = true
)

// This test checks the tree for log consistency by exercising the life-cycle of a tree,
// from creating a new tree from scratch, adding new nodes, flushing it to Store then
// adding more nodes to a new tree and having to load it from the Store.
func TestPutNodes(t *testing.T) {
	cfg := newConfigForTesting(t)
	tree := New(userName, cfg)

	dir1 := newDirEntry("/", isDir, cfg)
	err := tree.Put(dir1)
	if err != nil {
		t.Fatal(err)
	}
	dir2 := newDirEntry("/dir", isDir, cfg)
	err = tree.Put(dir2)
	if err != nil {
		t.Fatal(err)
	}
	dir3 := newDirEntry("/dir/doc.pdf", !isDir, cfg)
	err = tree.Put(dir3)
	if err != nil {
		t.Fatal(err)
	}

	// Verify three log entries were written.
	if got, want := cfg.Log.LastOffset(), uint64(3); got != want {
		t.Fatalf("LastIndex = %d, want %d", got, want)
	}
	entries, _, err := cfg.Log.Read(uint64(0), 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entries[0], *dir1) {
		t.Errorf("dir1 = %v, want %v", entries[0], dir1)
	}
	if !reflect.DeepEqual(entries[1], *dir2) {
		t.Errorf("dir2 = %v, want %v", entries[1], dir2)
	}
	if !reflect.DeepEqual(entries[2], *dir3) {
		t.Errorf("dir3 = %v, want %v", entries[2], dir3)
	}

	// Lookup path.
	de, dirty, err := tree.Lookup(userName + "/dir/doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want %v", dirty, true)
	}
	if !reflect.DeepEqual(de, *dir3) {
		t.Errorf("de = %v, want %v", de, dir3)
	}

	// Flush to later build a new tree and verify new is equivalent to old.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// New log index shows we're now at the end of the log.
	got, err := cfg.LogIndex.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if want := cfg.Log.LastOffset(); got != want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want %d", got, want)
	}

	// Lookup now returns !dirty.
	de, dirty, err = tree.Lookup(userName + "/dir/doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("dirty = %v, want %v", dirty, false)
	}
	if got, want := de.Name, dir3.Name; got != want {
		t.Errorf("de.Name = %v, want %v", de.Name, want)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2 := New(userName, cfg)
	log.Printf("========= Marker")

	log.Printf("=====Tree:\n%s\n", tree2.String())
	dir4 := newDirEntry("/dir/img.jpg", !isDir, cfg)
	err = tree2.Put(dir4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Log.LastOffset(), uint64(4); got != want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want %d", cfg.Log.LastOffset(), want)
	}

	log.Printf("=====Tree:\n%s\n", tree2.String())

	// Delete dir4.
	err = tree2.Delete(userName + "/dir/img.jpg")
	if err != nil {
		t.Fatal(err)
	}
	// Lookup won't return it.
	_, _, err = tree2.Lookup(userName + "/dir/img.jpg")
	expectedErr := errors.E("Delete", errors.NotExist, upspin.PathName(userName+"/dir/img.jpg"))
	if errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}
	// One new entry was written to the log (an updated dir2).
	if got, want := cfg.Log.LastOffset(), uint64(5); got != want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want %d", cfg.Log.LastOffset(), want)
	}
	// Verify logged entry is a new dir2
	entries, _, err = cfg.Log.Read(uint64(4), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entries[0].Name, upspin.PathName(userName+"/dir"); got != want {
		t.Errorf("entries[0].Name = %s, want = %s", got, want)
	}
}

// Test that an empty root can be saved and retrieved.
// Roots are handled differently than other directory entries.
func TestPutEmptyRoot(t *testing.T) {
	cfg := newConfigForTesting(t)
	tree := New(userName, cfg)

	dir1 := newDirEntry("/", isDir, cfg)
	err := tree.Put(dir1)
	if err != nil {
		t.Fatal(err)
	}

	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2 := New(userName, cfg)

	dir2 := newDirEntry("/dir", isDir, cfg)
	err = tree2.Put(dir2)
	if err != nil {
		t.Fatal(err)
	}

	// Try to put a file under an non-existent dir
	dir3 := newDirEntry("/invaliddir/myfile", !isDir, cfg)
	err = tree2.Put(dir3)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	expectedErr := errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want %s", err, expectedErr)
	}
}

// TestRebuildFromLog creates a tree and simulates a crash while there are
// entries that were not flushed to the Store. It tests that the new tree
// recovers from the log and is fully functional.
func TestRebuildFromLog(t *testing.T) {
	cfg := newConfigForTesting(t)
	tree := New(userName, cfg)

	de := newDirEntry("/", isDir, cfg)
	err := tree.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de = newDirEntry("/file1.txt", !isDir, cfg)
	err = tree.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de = newDirEntry("/dir1", isDir, cfg)
	err = tree.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}
	// Write more stuff after flush.
	de = newDirEntry("/file2.txt", !isDir, cfg)
	err = tree.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de = newDirEntry("/dir1/file_in_dir.txt", !isDir, cfg)
	err = tree.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	// And delete another
	err = tree.Delete(userName + "/file1.txt")
	if err != nil {
		t.Fatal(err)
	}

	// Now we crash and restart.
	// file2 must exist after recovery and file1 must not.
	tree = New(userName, cfg)
	_, dirty, err := tree.Lookup(userName + "/file2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want = true", dirty)
	}
	_, _, err = tree.Lookup(userName + "/file1.txt")
	expectedErr := errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want %s", err, expectedErr)
	}
}

// TODO: TestPutLargeNode: test that a huge DirEntry (>blockSize) gets split into multiple ones.
// TODO: Run all tests in loop using Plain and Debug packs as well.
// TODO: test more error cases.

// newDirEntry returns a dir entry for a path name filled with the mandatory
// arguments. It is used to make tests more concise.
func newDirEntry(name upspin.PathName, isDir bool, cfg *Config) *upspin.DirEntry {
	var writer upspin.UserName
	var attr upspin.FileAttributes
	if isDir {
		writer = serverName
		attr = upspin.AttrDirectory
	} else {
		writer = userName
		attr = upspin.AttrNone
	}
	return &upspin.DirEntry{
		Name:    userName + name,
		Attr:    attr,
		Packing: cfg.Context.Packing(),
		Writer:  writer,
	}
}

// newConfigForTesting creates a config with mocks, fakes, inprocess and otherwise testing
// versions of the Tree's dependencies.
func newConfigForTesting(t *testing.T) *Config {
	factotum, err := factotum.New(repo("key/testdata/upspin-test"))
	if err != nil {
		t.Fatal(err)
	}
	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	context := context.New().
		SetFactotum(factotum).
		SetUserName(serverName).
		SetStoreEndpoint(endpointInProcess).
		SetKeyEndpoint(endpointInProcess).
		SetPacking(upspin.EEPack)
	key := context.KeyServer()
	testKey, ok := key.(*inprocess.Service)
	if !ok {
		t.Fatal(err)
	}
	// Set the public key for the tree, since it must do Auth against the Store.
	testKey.SetPublicKeys(serverName, []upspin.PublicKey{factotum.PublicKey()})

	// Set the public key for the user, since EE Pack requires the dir owner to have a wrapped key.
	// TODO: re-think this for directories, but probably correct as-is because if the dir server goes
	// rogue or fails, the user can always run a dir server locally as himself and retrieve dir blocks.
	testKey.SetPublicKeys(userName, []upspin.PublicKey{factotum.PublicKey()})

	return &Config{
		Context: context,
		Log: &fakeLog{
			user: userName,
		},
		LogIndex: &fakeLogIndex{
			user: userName,
		},
	}
}

// fakeLog implements a simple, in-memory Log for testing.
type fakeLog struct {
	user       upspin.UserName
	dirEntries []upspin.DirEntry
}

var _ Log = (*fakeLog)(nil)

// fakeLogIndex implements a simple, in-memory LogIndex for testing.
type fakeLogIndex struct {
	user       upspin.UserName
	root       upspin.DirEntry
	lastOffset uint64
	hasRoot    bool
}

var _ LogIndex = (*fakeLogIndex)(nil)

// User returns the user name for whom this log logs.
func (l *fakeLog) User() upspin.UserName {
	return l.user
}

// Append appends a DirEntry at the end of the log.
func (l *fakeLog) Append(de *upspin.DirEntry) error {
	l.dirEntries = append(l.dirEntries, *de)
	return nil
}

// Read reads at most n entries from the log starting at index.
func (l *fakeLog) Read(offset uint64, n int) ([]upspin.DirEntry, uint64, error) {
	if int(offset)+n > len(l.dirEntries) {
		n = len(l.dirEntries) - int(offset)
	}
	return l.dirEntries[int(offset) : int(offset)+n], offset + uint64(n), nil
}

// LastOffset returns the offset after the most-recently-appended entry.
func (l *fakeLog) LastOffset() uint64 {
	return uint64(len(l.dirEntries))
}

// Root returns the location of the user's root.
func (l *fakeLogIndex) Root() (*upspin.DirEntry, error) {
	if l.hasRoot {
		return &l.root, nil
	}
	return nil, errors.E(errors.NotExist)
}

// SaveRoot saves the user's root.
func (l *fakeLogIndex) SaveRoot(r *upspin.DirEntry) error {
	l.root = *r
	l.hasRoot = true
	return nil
}

// User returns the user name who owns the root of the tree that this
// log index represents.
func (l *fakeLogIndex) User() upspin.UserName {
	return l.user
}

// ReadOffset reads from stable storage the offset saved by SaveOffset.
func (l *fakeLogIndex) ReadOffset() (uint64, error) {
	return l.lastOffset, nil
}

// SaveOffset saves to stable storage the last offset processed.
func (l *fakeLogIndex) SaveOffset(offs uint64) error {
	l.lastOffset = offs
	return nil
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
