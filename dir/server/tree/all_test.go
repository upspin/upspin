// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/key/inprocess"
	_ "upspin.io/pack/ee"
	_ "upspin.io/store/inprocess"
)

const (
	userName   = "user@domain.com"
	serverName = "tree@server.com"
	isDir      = true
)

var errNotExist = errors.E(errors.NotExist)

// This test checks the tree for log consistency by exercising the life-cycle of a tree,
// from creating a new tree from scratch, adding new nodes, flushing it to Store then
// adding more nodes to a new tree and having to load it from the Store.
func TestPutNodes(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tree.Put(newDirEntry("/", isDir, context))
	if err != nil {
		t.Fatal(err)
	}
	dir2, err := tree.Put(newDirEntry("/dir", isDir, context))
	if err != nil {
		t.Fatal(err)
	}
	dir3, err := tree.Put(newDirEntry("/dir/doc.pdf", !isDir, context))
	if err != nil {
		t.Fatal(err)
	}
	totBytes := entrySize(t, dir2) + entrySize(t, dir3)

	// Verify three log entries were written.
	if got, want := log.LastOffset(), int64(totBytes); got < want {
		t.Fatalf("LastIndex = %d, want > %d", got, want)
	}
	entries, _, err := log.ReadAt(2, int64(0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(entries), 2; got != want {
		t.Errorf("len(entries) = %d, want = %d", got, want)
	}
	if !reflect.DeepEqual(&entries[0].Entry, dir2) {
		t.Errorf("dir2 = %v, want %v", entries[0].Entry, dir2)
	}
	if !reflect.DeepEqual(&entries[1].Entry, dir3) {
		t.Errorf("dir3 = %v, want %v", entries[1].Entry, dir3)
	}

	// Lookup path.
	de, dirty, err := tree.Lookup(mkpath(t, userName+"/dir/doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	// Files are never dirty (they're packed and saved by the Client).
	// Only their parent dirs are dirty (tested elsewhere).
	if dirty {
		t.Errorf("dirty = %v, want %v", dirty, false)
	}
	if !reflect.DeepEqual(de, dir3) {
		t.Errorf("de = %v, want %v", de, dir3)
	}

	// Flush to later build a new tree and verify new is equivalent to old.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// New log index shows we're now at the end of the log.
	got, err := logIndex.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if want := log.LastOffset(); got != want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want %d", got, want)
	}

	// Lookup now returns !dirty.
	de, dirty, err = tree.Lookup(mkpath(t, userName+"/dir/doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("dirty = %v, want %v", dirty, false)
	}
	if got, want := de.Name, dir3.Name; got != want {
		t.Errorf("de.Name = %v, want %v", de.Name, want)
	}

	// Verify that the entry for the directory now has an incremented
	// sequence number.
	de, _, err = tree.Lookup(mkpath(t, userName+"dir/"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := de.Sequence, int64(upspin.SeqBase+1); got != want {
		t.Errorf("de.Sequence = %d, want = %d", got, want)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("== Tree:\n%s\n", tree2.String())
	dir4, err := tree2.Put(newDirEntry("/dir/img.jpg", !isDir, context))
	if err != nil {
		t.Fatal(err)
	}
	totBytes += entrySize(t, dir4)
	if got, want := log.LastOffset(), int64(totBytes); got < want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want > %d", log.LastOffset(), want)
	}

	// Try to lookup a file inside a file (checks a corner case bug).
	_, _, err = tree2.Lookup(mkpath(t, userName+"/dir/img.jpg/subfile"))
	expectedErr := errNotExist
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	t.Logf("== Tree:\n%s\n", tree2.String())

	// Delete dir4. Save offset before deleting.
	last := log.LastOffset()
	_, err = tree2.Delete(mkpath(t, userName+"/dir/img.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	// Lookup won't return it.
	_, _, err = tree2.Lookup(mkpath(t, userName+"/dir/img.jpg"))
	expectedErr = errNotExist
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}
	// One new entry was written to the log (an updated dir2).
	if got, want := log.LastOffset(), int64(totBytes); got < want {
		t.Fatalf("cfg.Log.LastIndex() = %d, want %d", log.LastOffset(), want)
	}
	// Verify logged entry is the deletion of a file.
	entries, _, err = log.ReadAt(1, last)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want = 1", len(entries))
	}
	if got, want := entries[0].Entry.Name, upspin.PathName(userName+"/dir/img.jpg"); got != want {
		t.Errorf("entries[0].Name = %s, want = %s", got, want)
	}
	if got, want := entries[0].Op, Delete; got != want {
		t.Errorf("entries[0].Op = %v, want = %v", got, want)
	}
}

func TestAddKidToEmptyNonDirtyDir(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, de := newDirEntry("/", isDir, context)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
	p, de = newDirEntry("/dir", isDir, context)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
	// Look for a path that doesn't exist yet (exercises a previous bug).
	_, _, err = tree.Lookup(mkpath(t, userName+"/dir/Access"))
	expectedErr := errNotExist
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}
	p, de = newDirEntry("/dir/subdir", isDir, context)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
}

// Test that an empty root can be saved and retrieved.
// Roots are handled differently than other directory entries.
func TestPutEmptyRoot(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, dir1 := newDirEntry("/", isDir, context)
	_, err = tree.Put(p, dir1)
	if err != nil {
		t.Fatal(err)
	}

	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, dir2 := newDirEntry("/dir", isDir, context)
	_, err = tree2.Put(p, dir2)
	if err != nil {
		t.Fatal(err)
	}

	// Try to put a file under an non-existent dir
	p, dir3 := newDirEntry("/invaliddir/myfile", !isDir, context)
	_, err = tree2.Put(p, dir3)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	expectedErr := errNotExist
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want %s", err, expectedErr)
	}

	// Try to delete the root.
	_, err = tree2.Delete(mkpath(t, userName+"/"))
	expectedErr = errors.E(errors.NotEmpty)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Delete dir and root.
	_, err = tree2.Delete(mkpath(t, userName+"/dir"))
	if err != nil {
		t.Fatal(err)
	}
	// Now it succeeds.
	_, err = tree2.Delete(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree2.Delete(mkpath(t, userName+"/"))
	expectedErr = errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Can't Put to tree2 anymore.
	_, err = tree2.Put(newDirEntry("/newdir", isDir, context))
	expectedErr = errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// But we can create the root again.
	_, err = tree2.Put(newDirEntry("/", isDir, context))
	if err != nil {
		t.Fatal(err)
	}
}

// TestRebuildFromLog creates a tree and simulates a crash while there are
// entries that were not flushed to the Store. It tests that the new tree
// recovers from the log and is fully functional.
func TestRebuildFromLog(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  upspin.PathName
		isDir bool
	}{
		{"/", isDir},
		{"/file1.txt", !isDir},
		{"/dir0", isDir},
		{"/dir0/file_in_dir.txt", !isDir},
	}
	for _, test := range tests {
		p, de := newDirEntry(test.name, test.isDir, context)
		_, err := tree.Put(p, de)
		if err != nil {
			t.Fatalf("Creating %q, isDir %v: %s", test.name, test.isDir, err)
		}
	}

	offsetBeforeCrash, err := logIndex.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash and restart, without ever having flushed the tree.
	tree, err = New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}
	offsetAfterCrash, err := logIndex.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	// Log index does not move until we Flush.
	if offsetBeforeCrash != offsetAfterCrash {
		t.Fatalf("offsetAfterCrash = %d, want = %d", offsetAfterCrash, offsetBeforeCrash)
	}

	// Lookup a file.
	de, _, err := tree.Lookup(mkpath(t, userName+"/dir0/file_in_dir.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(de.Name), userName+"/dir0/file_in_dir.txt"; got != want {
		t.Errorf("de.Name = %q, want = %q", got, want)
	}

	// Flush to Store.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// Write more stuff after flush.
	tests = []struct {
		name  upspin.PathName
		isDir bool
	}{
		{"/file2.txt", !isDir},
		{"/dir1", isDir},
		{"/dir1/file_in_dir.txt", !isDir},
	}
	for _, test := range tests {
		p, de := newDirEntry(test.name, test.isDir, context)
		_, err := tree.Put(p, de)
		if err != nil {
			t.Fatalf("Creating %q, isDir %v: %s", test.name, test.isDir, err)
		}
	}
	// And delete some others.
	_, err = tree.Delete(mkpath(t, userName+"/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Delete(mkpath(t, userName+"/dir0/file_in_dir.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Now we crash and restart.
	// file2 and file_in_dir must exist after recovery and file1 must not.
	tree, err = New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	// Files are never dirty (they're packed and saved by the Client),
	// only dirs are dirty.
	_, dirty, err := tree.Lookup(mkpath(t, userName))
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want = true", dirty)
	}
	_, dirty, err = tree.Lookup(mkpath(t, userName+"/dir1"))
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want = true", dirty)
	}

	_, _, err = tree.Lookup(mkpath(t, userName+"/file1.txt"))
	expectedErr := errNotExist
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want %s", err, expectedErr)
	}
	_, _, err = tree.Lookup(mkpath(t, userName+"/dir0/file_in_dir.txt"))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want %s", err, expectedErr)
	}

	// Furthermore, we can create entries in an existing directory.
	p, de := newDirEntry("/dir0/will_not_fail", !isDir, context)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutLargeNode(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, dir1 := newDirEntry("/", isDir, context)
	_, err = tree.Put(p, dir1)
	if err != nil {
		t.Fatal(err)
	}
	p, dir2 := newDirEntry("/largefile", !isDir, context)
	dir2.Packdata = make([]byte, blockSize+1) // force a block split on the next file.
	_, err = tree.Put(p, dir2)
	if err != nil {
		t.Fatal(err)
	}
	p, dir3 := newDirEntry("/smallfile", !isDir, context)
	_, err = tree.Put(p, dir3)
	if err != nil {
		t.Fatal(err)
	}
	err = tree.Flush() // force writing blocks to Store.
	if err != nil {
		t.Fatal(err)
	}
	root, err := tree.Root()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(root.Blocks), 2; got != want {
		t.Errorf("len(root.Blocks) = %d, want = %d, root=%v", got, want, root)
	}
}

func TestLinks(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}
	// Make root.
	p, deRoot := newDirEntry("/", isDir, context)
	_, err = tree.Put(p, deRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Test links in subdirectories, starting right at the root and going
	// down, for a comprehensive check.
	for _, dir := range []upspin.PathName{"", "/mysubdir", "/mysubdir/deeper"} {
		if dir != "" {
			p, deSub := newDirEntry(dir, isDir, context)
			_, err = tree.Put(p, deSub)
			if err != nil {
				t.Fatal(err)
			}
		}

		// Put a link.
		p, deLink := newDirEntry(dir+"/link", !isDir, context)
		deLink.Attr = upspin.AttrLink
		deLink.Link = "linkerdude@link.lnk/the_target"
		_, err = tree.Put(p, deLink)
		if err != nil {
			t.Fatal(err)
		}

		// Try to put a file inside the link.
		p, deFile := newDirEntry(dir+"/link/file.txt", !isDir, context)
		got, err := tree.Put(p, deFile)
		if err != upspin.ErrFollowLink {
			t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
		}
		if got.Link != deLink.Link {
			t.Errorf("got.Link = %q, want = %q", got.Link, deLink.Link)
		}

		// Try to read something with a link.
		got, _, err = tree.Lookup(mkpath(t, userName+dir+"/link/subdir/more/cookie.jar"))
		if err != upspin.ErrFollowLink {
			t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
		}
		if got.Link != deLink.Link {
			t.Errorf("got.Link = %q, want = %q", got.Link, deLink.Link)
		}

		// Lookup the link itself.
		got, _, err = tree.Lookup(mkpath(t, userName+dir+"/link"))
		if err != nil {
			t.Fatal(err)
		}
		if !got.IsLink() {
			t.Error("Expected link")
		}

		// Start a new tree to ensure links are recovered from the log.
		tree, err = New(context, log, logIndex)
		if err != nil {
			t.Fatal(err)
		}

		// Now try to delete something inside the link's path.
		got, err = tree.Delete(mkpath(t, userName+dir+"/link/deep/inside/your/soul"))
		if err != upspin.ErrFollowLink {
			t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
		}
		if got.Link != deLink.Link {
			t.Errorf("got.Link = %q, want = %q", got.Link, deLink.Link)
		}

		// Delete the link.
		_, err = tree.Delete(mkpath(t, userName+dir+"/link"))
		if err != nil {
			t.Fatal(err)
		}

		// Looking up inside link now returns a normal error.
		_, _, err = tree.Lookup(mkpath(t, userName+dir+"/link/subdir/more/cookie.jar"))
		expectedErr := errNotExist
		if !errors.Match(expectedErr, err) {
			t.Errorf("err = %v, want = %v", err, expectedErr)
		}
	}

	// Flush the whole thing just for fun.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	for _, dir := range []upspin.PathName{"/", "/dir1", "/dir2", "/dir3", "/dir3/sub1"} {
		p, de := newDirEntry(dir, isDir, context)
		_, err = tree.Put(p, de)
		if err != nil {
			t.Fatal(err)
		}
	}
	// List root.
	root, err := path.Parse(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	entries, dirty, err := tree.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want = true", dirty)
	}
	if got, want := len(entries), 3; got != want {
		t.Fatalf("len(entries) = %d, want = %d", got, want)
	}
	expected := map[upspin.PathName]bool{
		userName + "/dir1": true,
		userName + "/dir2": true,
		userName + "/dir3": true,
	}
	for _, e := range entries {
		if _, found := expected[e.Name]; !found {
			t.Errorf("e.Name = %q, want = one-of {%v}", e.Name, expected)
		}
	}

	// List dir3.
	subdir, err := path.Parse(userName + "/dir3")
	if err != nil {
		t.Fatal(err)
	}
	entries, dirty, err = tree.List(subdir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want = true", dirty)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want = %d", len(entries), 1)
	}
	if got, want := entries[0].Name, upspin.PathName(userName+"/dir3/sub1"); got != want {
		t.Errorf("entries[0].Name = %q, want = %q", got, want)
	}

	// Flush and check no entry is dirty anymore.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}
	entries, dirty, err = tree.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("dirty = %v, want = false", dirty)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want = %d", len(entries), 3)
	}
}

var topDir string // where we write our test data.

func TestMain(m *testing.M) {
	var err error
	topDir, err = ioutil.TempDir("", "Tree")
	if err != nil {
		panic(err)
	}

	code := m.Run()

	os.RemoveAll(topDir)
	os.Exit(code)
}

// TODO: Run all tests in loop using Plain and Debug packs as well.
// TODO: test more error cases.

func mkpath(t *testing.T, pathName upspin.PathName) path.Parsed {
	p, err := path.Parse(pathName)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newDirEntry returns a dir entry for a path name filled with the mandatory
// arguments. It is used to make tests more concise.
func newDirEntry(name upspin.PathName, isDir bool, context upspin.Context) (path.Parsed, *upspin.DirEntry) {
	var writer upspin.UserName
	var attr upspin.Attribute
	if isDir {
		writer = serverName
		attr = upspin.AttrDirectory
	} else {
		writer = userName
		attr = upspin.AttrNone
	}
	p, err := path.Parse(userName + name)
	if err != nil {
		panic(err)
	}
	return p, &upspin.DirEntry{
		Name:     p.Path(),
		Attr:     attr,
		Packing:  context.Packing(),
		Writer:   writer,
		Packdata: []byte("1234"),
	}
}

// newConfigForTesting creates the necessary items to instantiate a Tree for
// testing.
func newConfigForTesting(t *testing.T) (upspin.Context, *Log, *LogIndex) {
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
	// Set the public key for the tree, since it must do Auth against the Store.
	user := &upspin.User{
		Name:      serverName,
		Dirs:      []upspin.Endpoint{context.DirEndpoint()},
		Stores:    []upspin.Endpoint{context.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Set the public key for the user, since EE Pack requires the dir owner to have a wrapped key.
	// TODO: re-think this for directories, but probably correct as-is because if the dir server goes
	// rogue or fails, the user can always run a dir server locally as himself and retrieve dir blocks.
	user = &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{context.DirEndpoint()},
		Stores:    []upspin.Endpoint{context.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}
	// Create a new tempdir as a subdir of topdir, so it gets garbage
	// collected at the very end.
	tmpDir, err := ioutil.TempDir(topDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	log.Printf("tmpDir: %s", tmpDir)
	log, logIndex, err := NewLogs(userName, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	err = logIndex.SaveOffset(0)
	if err != nil {
		t.Fatal(err)
	}
	return context, log, logIndex
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		panic("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}

func entrySize(t *testing.T, entry *upspin.DirEntry) int {
	buf, err := entry.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return len(buf)
}
