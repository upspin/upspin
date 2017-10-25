// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"
	"time"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/dir/server/serverlog"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/path"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"

	_ "upspin.io/pack/ee"
)

var errNotExist = errors.E(errors.NotExist)

func init() {
	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, storeserver.New())
}

const (
	userName   = "user@domain.com"
	serverName = "tree@server.com"
	isDir      = true
)

// This test checks the tree for log consistency by exercising the life-cycle of a tree,
// from creating a new tree from scratch, adding new nodes, flushing it to Store then
// adding more nodes to a new tree and having to load it from the Store.
func TestPutNodes(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	seq := int64(upspin.SeqBase)
	root, err := tree.Put(newDirEntry("/", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	seq++
	dir2, err := tree.Put(newDirEntry("/dir", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	seq++
	dir3, err := tree.Put(newDirEntry("/dir/doc.pdf", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	totBytes := entrySize(t, dir2) + entrySize(t, dir3)
	// Root Sequence should be updated.
	p, err := path.Parse(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	de, _, err := tree.Lookup(p)
	if err != nil {
		t.Fatal(err)
	}
	if de.Sequence != seq {
		t.Fatalf("%s: got sequence %d; expected %d", p, de.Sequence, seq)
	}

	// Verify three log entries were written.
	if got, want := user.AppendOffset(), int64(totBytes); got < want {
		t.Fatalf("LastOffset = %d, want > %d", got, want)
	}
	lrd, err := user.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	entry, next, err := lrd.ReadAt(int64(0))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&entry.Entry, dir2) {
		t.Errorf("dir2 = %v, want %v", entry.Entry, dir2)
	}
	entry, _, err = lrd.ReadAt(next)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&entry.Entry, dir3) {
		t.Errorf("dir3 = %v, want %v", entry.Entry, dir3)
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

	de, dirty, err = tree.Lookup(mkpath(t, userName+"/dir"))
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Errorf("dirty = %v, want %v", dirty, true)
	}
	if de.Sequence != seq {
		t.Errorf("de.Sequence = %d, want = %d", de.Sequence, seq)
	}

	// Flush to later build a new tree and verify new is equivalent to old.
	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	newRoot, _, err := tree.Lookup(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	if newRoot.Time <= root.Time {
		t.Fatalf("Time moved backwards: got %d, want > %d", newRoot.Time, root.Time)
	}
	if newRoot.Sequence != seq {
		t.Errorf("newRoot.Sequence = %d, want = %d", newRoot.Sequence, seq)
	}

	// New log index shows we're now at the end of the log.
	got, err := user.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	if want := user.AppendOffset(); got != want {
		t.Fatalf("AppendOffset() = %d, want %d", got, want)
	}

	// Lookup now returns !dirty.
	de, dirty, err = tree.Lookup(mkpath(t, userName+"/dir/doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("dirty = %v, want %v", dirty, false)
	}
	if de.Name != dir3.Name {
		t.Errorf("de.Name = %v, want %v", de.Name, dir3.Name)
	}

	// Verify that the entry for the directory still has the incremented
	// sequence number.
	de, dirty, err = tree.Lookup(mkpath(t, userName+"/dir/"))
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("dirty = %v, want %v", dirty, false)
	}
	if de.Sequence != seq {
		t.Errorf("de.Sequence = %d, want = %d", de.Sequence, seq)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("== Tree:\n%s", tree2.String())
	dir4, err := tree2.Put(newDirEntry("/dir/img.jpg", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	totBytes += entrySize(t, dir4)
	if got, want := user.AppendOffset(), int64(totBytes); got < want {
		t.Fatalf("AppendOffset() = %d, want > %d", got, want)
	}

	// Try to lookup a file inside a file (checks a corner case bug).
	_, _, err = tree2.Lookup(mkpath(t, userName+"/dir/img.jpg/subfile"))
	expectedErr := errNotExist
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	t.Logf("== Tree:\n%s", tree2.String())

	// Delete dir4. Save offset before deleting.
	last := user.AppendOffset()
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
	if got, want := user.AppendOffset(), int64(totBytes); got < want {
		t.Fatalf("AppendOffset() = %d, want %d", got, want)
	}
	// Verify logged entry is the deletion of a file.
	entry, _, err = lrd.ReadAt(last)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry.Entry.Name, upspin.PathName(userName+"/dir/img.jpg"); got != want {
		t.Errorf("entries[0].Name = %s, want = %s", got, want)
	}
	if got, want := entry.Op, serverlog.Delete; got != want {
		t.Errorf("entries[0].Op = %v, want = %v", got, want)
	}
}

func TestAddKidToEmptyNonDirtyDir(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	p, de := newDirEntry("/", isDir, config)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
	p, de = newDirEntry("/dir", isDir, config)
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
	p, de = newDirEntry("/dir/subdir", isDir, config)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
}

// Test that an empty root can be saved and retrieved.
// Roots are handled differently than other directory entries.
func TestPutEmptyRoot(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	p, dir1 := newDirEntry("/", isDir, config)
	_, err = tree.Put(p, dir1)
	if err != nil {
		t.Fatal(err)
	}

	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	// Now start a new tree from scratch and confirm it is loaded from the Store.
	tree2, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	p, dir2 := newDirEntry("/dir", isDir, config)
	_, err = tree2.Put(p, dir2)
	if err != nil {
		t.Fatal(err)
	}

	// Try to put a file under an non-existent dir
	p, dir3 := newDirEntry("/invaliddir/myfile", !isDir, config)
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
	_, err = tree2.Put(newDirEntry("/newdir", isDir, config))
	expectedErr = errors.E(errors.NotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// But we can create the root again.
	_, err = tree2.Put(newDirEntry("/", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
}

// TestRebuildFromLog creates a tree and simulates a crash while there are
// entries that were not flushed to the Store. It tests that the new tree
// recovers from the log and is fully functional.
func TestRebuildFromLog(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
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
		p, de := newDirEntry(test.name, test.isDir, config)
		_, err := tree.Put(p, de)
		if err != nil {
			t.Fatalf("Creating %q, isDir %v: %s", test.name, test.isDir, err)
		}
	}

	offsetBeforeCrash, err := user.ReadOffset()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash and restart, without ever having flushed the tree.
	tree, err = New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	offsetAfterCrash, err := user.ReadOffset()
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
		p, de := newDirEntry(test.name, test.isDir, config)
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
	deleted, err := tree.Delete(mkpath(t, userName+"/dir0/file_in_dir.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Now we crash and restart.
	// /file2.txt and /dir1/file_in_dir must exist after recovery and /file1
	// must not.
	tree, err = New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Verify that all the sequence numbers are set to the most recent value.
	if seq := tree.sequence; seq != deleted.Sequence {
		t.Fatalf("tree sequence is %d; expected %d", seq, deleted.Sequence)
	}
	if seq := tree.root.entry.Sequence; seq != deleted.Sequence {
		t.Fatalf("root sequence is %d; expected %d", seq, deleted.Sequence)
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
	p, de := newDirEntry("/dir0/will_not_fail", !isDir, config)
	_, err = tree.Put(p, de)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutLargeNode(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	p, dir1 := newDirEntry("/", isDir, config)
	_, err = tree.Put(p, dir1)
	if err != nil {
		t.Fatal(err)
	}
	p, dir2 := newDirEntry("/largefile", !isDir, config)
	dir2.Packdata = make([]byte, upspin.BlockSize+1) // force a block split on the next file.
	_, err = tree.Put(p, dir2)
	if err != nil {
		t.Fatal(err)
	}
	p, dir3 := newDirEntry("/smallfile", !isDir, config)
	_, err = tree.Put(p, dir3)
	if err != nil {
		t.Fatal(err)
	}
	err = tree.Flush() // force writing blocks to Store.
	if err != nil {
		t.Fatal(err)
	}
	// Reach inside, read the root entry and verify its blocks.
	root, err := tree.user.Root()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(root.Blocks), 2; got != want {
		t.Errorf("len(root.Blocks) = %d, want = %d, root=%v", got, want, root)
	}
}

func TestLinks(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Make root.
	p, deRoot := newDirEntry("/", isDir, config)
	_, err = tree.Put(p, deRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Test links in subdirectories, starting right at the root and going
	// down, for a comprehensive check.
	for _, dir := range []upspin.PathName{"", "/mysubdir", "/mysubdir/deeper"} {
		if dir != "" {
			p, deSub := newDirEntry(dir, isDir, config)
			_, err = tree.Put(p, deSub)
			if err != nil {
				t.Fatal(err)
			}
		}

		// Put a link.
		p, deLink := newDirEntry(dir+"/link", !isDir, config)
		deLink.Attr = upspin.AttrLink
		deLink.Link = "linkerdude@link.lnk/the_target"
		_, err = tree.Put(p, deLink)
		if err != nil {
			t.Fatal(err)
		}

		// Try to put a file inside the link.
		p, deFile := newDirEntry(dir+"/link/file.txt", !isDir, config)
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
		tree, err = New(config, user)
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
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	for _, dir := range []upspin.PathName{"/", "/dir1", "/dir2", "/dir3", "/dir3/sub1"} {
		p, de := newDirEntry(dir, isDir, config)
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

func TestPutDirSameTreeNonRoot(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	buildTree(t, tree, config)

	t.Logf("Tree1:\n%s", tree)

	// Lookup orig, which we'll PutDir under /snapshot/new.
	entry, _, err := tree.Lookup(mkpath(t, userName+"/orig"))
	if err != nil {
		t.Fatal(err)
	}
	// Put entry under a new directory.
	_, err = tree.PutDir(mkpath(t, userName+"/snapshot/new"), entry)
	if err != nil {
		t.Fatal(err)
	}

	err = tree.Flush()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Tree2:\n%s", tree)

	// List snapshot directory.
	entries, _, err := tree.List(mkpath(t, userName+"/snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	// Expected maps Name to SignedName.
	expected := map[upspin.PathName]upspin.PathName{
		userName + "/snapshot/new": userName + "/orig",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}

	// List snapshot.
	entries, _, err = tree.List(mkpath(t, userName+"/snapshot/new"))
	if err != nil {
		t.Fatal(err)
	}
	// Here we expect some "redirection" to happen.
	expected = map[upspin.PathName]upspin.PathName{
		userName + "/snapshot/new/sub1": userName + "/orig/sub1",
		userName + "/snapshot/new/sub2": userName + "/orig/sub2",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Tree3:\n%s", tree)

	// List inside snapshot.
	entries, _, err = tree.List(mkpath(t, userName+"/snapshot/new/sub1"))
	if err != nil {
		t.Fatal(err)
	}
	expected = map[upspin.PathName]upspin.PathName{
		userName + "/snapshot/new/sub1/subsub":    userName + "/orig/sub1/subsub",
		userName + "/snapshot/new/sub1/file1.txt": userName + "/orig/sub1/file1.txt",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}

	// Create a new entry in the original place, to ensure it's not
	// reflected in the snapshot now.
	_, err = tree.Put(newDirEntry("/orig/file.txt", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Tree with new file:\n%s", tree)

	// List inside snapshot again.
	entries, _, err = tree.List(mkpath(t, userName+"/snapshot/new"))
	if err != nil {
		t.Fatal(err)
	}
	expected = map[upspin.PathName]upspin.PathName{
		userName + "/snapshot/new/sub1": userName + "/orig/sub1",
		userName + "/snapshot/new/sub2": userName + "/orig/sub2",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}

	// Try to PutDir again, it should fail with an "exists" error.
	_, err = tree.PutDir(mkpath(t, userName+"/snapshot/new"), entry)
	if err == nil {
		t.Fatal("PutDir of existing target: expected error, got nil")
	} else if !errors.Is(errors.Exist, err) {
		t.Fatalf("PutDir of existing target: expected 'exists' error, got %v", err)
	}

	// Create a new tree (simulate a crash).
	tree, err = New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	// List inside snapshot again.
	entries, _, err = tree.List(mkpath(t, userName+"/snapshot/new"))
	if err != nil {
		t.Fatal(err)
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutDirSameTreeRoot(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	buildTree(t, tree, config)

	t.Logf("Tree1:\n%s", tree)

	// Lookup root, which we'll PutDir under /snapshot/oldroot.
	entry, _, err := tree.Lookup(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	// The last element in the path must be new.
	_, err = tree.PutDir(mkpath(t, userName+"/snapshot/oldroot"), entry)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Tree2:\n%s", tree)

	// List inside snapshot again.
	entries, _, err := tree.List(mkpath(t, userName+"/snapshot/oldroot/orig/sub1"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[upspin.PathName]upspin.PathName{
		userName + "/snapshot/oldroot/orig/sub1/subsub":    userName + "/orig/sub1/subsub",
		userName + "/snapshot/oldroot/orig/sub1/file1.txt": userName + "/orig/sub1/file1.txt",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutDirOtherTreeRoot(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	buildTree(t, tree, config)

	t.Logf("Tree1:\n%s", tree)

	// Create another tree for another user.
	const otherUser = "other@another.biz"
	config2, user2 := newConfigForTesting(t, otherUser)
	tree2, err := New(config2, user2)
	if err != nil {
		t.Fatal(err)
	}
	// Create root for otherUser.
	p, err := path.Parse(otherUser + "/")
	if err != nil {
		t.Fatal(err)
	}
	entry := &upspin.DirEntry{
		Name:       p.Path(),
		SignedName: p.Path(),
		Attr:       upspin.AttrDirectory,
		Packing:    config2.Packing(),
		Writer:     serverName,
	}
	_, err = tree2.Put(p, entry)
	if err != nil {
		t.Fatal(err)
	}

	// Now lookup and add a snapshot directory backing up the root of
	// userName.
	root, _, err := tree.Lookup(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree2.PutDir(mkpath(t, otherUser+"/snap"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tree2:\n%s", tree2)

	// There's only snap under the root.
	entries, _, err := tree2.List(mkpath(t, otherUser+"/"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[upspin.PathName]upspin.PathName{
		otherUser + "/snap": userName + "/",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}

	// And all of the original tree under it.
	entries, _, err = tree2.List(mkpath(t, otherUser+"/snap"))
	if err != nil {
		t.Fatal(err)
	}
	expected = map[upspin.PathName]upspin.PathName{
		otherUser + "/snap/orig":     userName + "/orig",
		otherUser + "/snap/snapshot": userName + "/snapshot",
		otherUser + "/snap/other":    userName + "/other",
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}
	// Creating a new tree2 (crash and restart) yields the same thing.
	tree3, err := New(config2, user2)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err = tree3.List(mkpath(t, otherUser+"/snap"))
	if err != nil {
		t.Fatal(err)
	}
	err = checkDirList(entries, expected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutDirLog(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	buildTree(t, tree, config)

	dstDir := mkpath(t, userName+"/snapshot/new")

	// Look up "/orig" and put it to dstDir ("/snapshot/new").
	entry, _, err := tree.Lookup(mkpath(t, userName+"/orig"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.PutDir(dstDir, entry)
	if err != nil {
		t.Fatal(err)
	}

	// Find a log entry for "/snapshot/new".
	r, err := user.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	var offset, last int64 = 0, r.EndOffset()
	for offset < last {
		le, next, err := r.ReadAt(offset)
		if err != nil {
			t.Fatal(err)
		}
		if le.Entry.Name == dstDir.Path() {
			// Found.
			return
		}
		offset = next
	}
	t.Fatalf("could not find log entry for %q", dstDir)

}

func TestPutDirWatch(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Build a simple tree.
	buildTree(t, tree, config)

	// The directory to create with PutDir.
	dstDir := mkpath(t, userName+"/snapshot/new")

	// Watch the destination directory.
	done := make(chan struct{})
	defer close(done)
	events, err := tree.Watch(dstDir, 0, done)
	if err != nil {
		t.Fatal(err)
	}

	// Look up "/orig" and put it to dstDir ("/snapshot/new").
	entry, _, err := tree.Lookup(mkpath(t, userName+"/orig"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.PutDir(dstDir, entry)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for an Event for the creation of dstDir.
	select {
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for event")
	case e := <-events:
		if e.Error != nil {
			t.Fatal(err)
		}
		if e.Entry == nil {
			t.Fatal("got Event with nil Entry")
		}
		if got, want := e.Entry.Name, dstDir.Path(); got != want {
			t.Errorf("got Event with Name %q, want %q", got, want)
		}
	}
}

func TestFlushNewTree(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	err = tree.Flush()
	expectedErr := errors.E(errors.NotExist, upspin.UserName(userName))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}
}

func TestCorruptTreeAndRecover(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	// Create some directories first.
	for _, e := range []struct {
		name upspin.PathName
	}{
		{"/"},
		{"/dir1"},
		{"/dir2"},
	} {
		_, err = tree.Put(newDirEntry(e.name, isDir, config))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Write some garbage to the log.
	_, err = user.Write([]byte("Some garbage"))
	if err != nil {
		t.Fatal(err)
	}

	// Put a new directory.
	_, err = tree.Put(newDirEntry("/dir3", isDir, config))
	if err != nil {
		t.Fatal(err)
	}

	// Crash and recover the tree.
	tree2, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := tree2.List(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[upspin.PathName]upspin.PathName{
		userName + "/dir1": userName + "/dir1",
		userName + "/dir2": userName + "/dir2",
		// dir3 is lost given the log corruption.
	}
	err = checkDirList(entries, want)
	if err != nil {
		t.Fatal(err)
	}

	// Now we can put dir3 normally.
	_, err = tree2.Put(newDirEntry("/dir3", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	// Check again.
	entries, _, err = tree2.List(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	want[userName+"/dir3"] = userName + "/dir3"
	err = checkDirList(entries, want)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure reading the log again does not cause problems.
	tree3, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err = tree3.List(mkpath(t, userName+"/"))
	if err != nil {
		t.Fatal(err)
	}
	err = checkDirList(entries, want)
	if err != nil {
		t.Fatal(err)
	}
}

var topDir string // where we write our test data.

func TestMain(m *testing.M) {
	var err error
	topDir, err = ioutil.TempDir("", "Tree")
	if err != nil {
		panic(err)
	}

	// Make the logs rotate frequently.
	serverlog.MaxLogSize = 100

	code := m.Run()

	os.RemoveAll(topDir)
	os.Exit(code)
}

// TODO: Run all tests in loop using Plain and Debug packs as well.
// TODO: test more error cases.

func mkdir(t *testing.T, tree *Tree, cfg upspin.Config, name upspin.PathName) (path.Parsed, *upspin.DirEntry) {
	p, entry := newDirEntry(name, isDir, cfg)
	entry, err := tree.Put(p, entry)
	if err != nil {
		t.Fatal(err)
	}
	return p, entry
}

func checkDirList(got []*upspin.DirEntry, want map[upspin.PathName]upspin.PathName) error {
	if len(got) != len(want) {
		return errors.Errorf("len(got) = %d, want = %d", len(got), len(want))
	}
	for _, e := range got {
		if signedName, found := want[e.Name]; !found {
			var wantSlice []upspin.PathName
			for k := range want {
				wantSlice = append(wantSlice, k)
			}
			return errors.Errorf("e.Name = %q, want = one-of %v", e.Name, wantSlice)
		} else if e.SignedName != signedName {
			return errors.Errorf("e.SignedName = %q, want = %q", e.SignedName, signedName)
		}
	}
	return nil
}

func mkpath(t *testing.T, pathName upspin.PathName) path.Parsed {
	p, err := path.Parse(pathName)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newDirEntry returns a dir entry for a path name filled with the mandatory
// arguments. It is used to make tests more concise.
func newDirEntry(name upspin.PathName, isDir bool, config upspin.Config) (path.Parsed, *upspin.DirEntry) {
	var writer upspin.UserName
	var attr upspin.Attribute
	var blocks []upspin.DirBlock
	if isDir {
		writer = serverName
		attr = upspin.AttrDirectory
	} else {
		writer = userName
		attr = upspin.AttrNone
		blocks = []upspin.DirBlock{
			{
				Offset:   0,
				Size:     1024,
				Packdata: []byte("sign"),
			},
		}
	}
	p, err := path.Parse(userName + name)
	if err != nil {
		panic(err)
	}
	return p, &upspin.DirEntry{
		Name:       p.Path(),
		Sequence:   upspin.SeqIgnore,
		SignedName: p.Path(),
		Attr:       attr,
		Packing:    config.Packing(),
		Writer:     writer,
		Packdata:   []byte("1234"),
		Blocks:     blocks,
	}
}

// newConfigForTesting creates the necessary items to instantiate a Tree for
// testing.
func newConfigForTesting(t *testing.T, userName upspin.UserName) (upspin.Config, *serverlog.User) {
	factotum, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "test"))
	if err != nil {
		t.Fatal(err)
	}
	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	cfg := config.New()
	cfg = config.SetUserName(cfg, serverName)
	cfg = config.SetFactotum(cfg, factotum)
	cfg = config.SetStoreEndpoint(cfg, endpointInProcess)
	cfg = config.SetKeyEndpoint(cfg, endpointInProcess)
	cfg = config.SetPacking(cfg, upspin.EEPack)
	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	// Set the public key for the tree, since it must do Auth against the Store.
	user := &upspin.User{
		Name:      serverName,
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Set the public key for the user, since EE Pack requires the dir owner to have a wrapped key.
	user = &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
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
	serverlogUser, err := serverlog.Open(userName, tmpDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = serverlogUser.SaveOffset(0)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, serverlogUser
}

func entrySize(t *testing.T, entry *upspin.DirEntry) int {
	buf, err := entry.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return len(buf)
}

func buildTree(t *testing.T, tree *Tree, config upspin.Config) {
	// Create some directories and files.
	for _, e := range []struct {
		name upspin.PathName
		dir  bool
	}{
		{"/", isDir},
		{"/orig", isDir},
		{"/orig/sub1", isDir},
		{"/orig/sub2", isDir},
		{"/orig/sub1/subsub", isDir},
		{"/snapshot", isDir},
		{"/other", isDir},
		{"/orig/sub1/file1.txt", !isDir},
	} {
		p, de := newDirEntry(e.name, e.dir, config)
		_, err := tree.Put(p, de)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Flush, to ensure we have dirs that contain committed blocks.
	err := tree.Flush()
	if err != nil {
		t.Fatal(err)
	}
}
