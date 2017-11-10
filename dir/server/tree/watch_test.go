// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"testing"
	"time"

	"upspin.io/errors"
	"upspin.io/upspin"
)

const (
	isDelete  = true
	hasBlocks = true
)

func TestWatchStart(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	p, _ := mkdir(t, tree, config, "/")

	ch, err := tree.Watch(p, upspin.WatchStart, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}

	// Put something under the root and observe a notification.
	dirPath, dir := mkdir(t, tree, config, "/dir")

	event := <-ch
	err = checkEvent(event, dir.SignedName, !isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Put something under dir and observe another notification.
	subdirPath, subdir := mkdir(t, tree, config, "/dir/subdir")

	event = <-ch
	err = checkEvent(event, subdir.SignedName, !isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Delete an entry and observe the notification.
	_, err = tree.Delete(subdirPath)
	if err != nil {
		t.Fatal(err)
	}

	event = <-ch
	err = checkEvent(event, subdir.SignedName, isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Add a watcher to dir.
	done := make(chan struct{})
	ch2, err := tree.Watch(dirPath, upspin.WatchStart, done)
	if err != nil {
		t.Fatal(err)
	}

	// Put a file under dir. Watch two updates, one on each channel.
	p, entry := newDirEntry("/dir/fileA.txt", !isDir, config)
	_, err = tree.Put(p, entry)
	if err != nil {
		t.Fatal(err)
	}

	event = <-ch
	err = checkEvent(event, entry.SignedName, !isDelete, hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Watcher two will get the creation of /dir since it's starting from
	// zero and then subdir creation and deletion and fileA.
	for i, tc := range []struct {
		name      upspin.PathName
		isDelete  bool
		hasBlocks bool
	}{
		{dir.SignedName, !isDelete, !hasBlocks},
		{subdir.SignedName, !isDelete, !hasBlocks},
		{subdir.SignedName, isDelete, !hasBlocks},
		{entry.SignedName, !isDelete, hasBlocks},
	} {
		event = <-ch2
		err = checkEvent(event, tc.name, tc.isDelete, tc.hasBlocks)
		if err != nil {
			t.Errorf("%d: %s", i, err)
		}
	}

	// Stop watching ch2 and trigger another notification. This time only
	// one event is sent.
	close(done)
	// ch2 is now closed.
	event, ok := <-ch2
	if ok {
		t.Errorf("Expected channel closed, got event = %v", event)
	}

	p, entry = newDirEntry("/dir/fileB.txt", !isDir, config)
	_, err = tree.Put(p, entry)
	if err != nil {
		t.Fatal(err)
	}

	// First channel gets it.
	event = <-ch
	err = checkEvent(event, entry.SignedName, !isDelete, hasBlocks)
	if err != nil {
		t.Error(err)
	}
}

func TestWatchFromMiddle(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, config)

	// Generate a delete event.
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Watch for events that happened from a specific sequence onwards,
	// for a subdirectory. The magic number below (4) is the sequence
	// number of "mkdir /orig/sub2/".
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), 4, done)
	if err != nil {
		t.Fatal(err)
	}

	for i, exp := range []struct {
		name      upspin.PathName
		isDelete  bool
		hasBlocks bool
	}{
		{"/orig/sub1/subsub", !isDelete, !hasBlocks},
		{"/orig/sub1/file1.txt", !isDelete, hasBlocks},
		{"/orig/sub1/file1.txt", isDelete, hasBlocks},
	} {
		event := <-ch
		err = checkEvent(event, userName+exp.name, exp.isDelete, exp.hasBlocks)
		if err != nil {
			t.Errorf("%d: %s", i, err)
		}
	}

	// No further events.
	select {
	case event := <-ch:
		t.Errorf("Expected no event, got %v", event)
	case <-time.After(10 * time.Millisecond):
		// Ok. Nothing should ever show up.
	}
}

func TestWatchCurrent(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, config)

	// Get a watcher for the current subtree, rooted at orig/sub1.
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), upspin.WatchCurrent, done)
	if err != nil {
		t.Fatal(err)
	}

	// Make further modifications.
	_, err = tree.Put(newDirEntry("/orig/sub1/thesis.pdf", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// The Watcher will give us the current subtree and continue to give us
	// new updates.
	for i, exp := range []struct {
		name      upspin.PathName
		isDelete  bool
		hasBlocks bool
	}{
		{"/orig/sub1", !isDelete, !hasBlocks},
		{"/orig/sub1/file1.txt", !isDelete, hasBlocks},
		{"/orig/sub1/subsub", !isDelete, !hasBlocks},
		{"/orig/sub1/thesis.pdf", !isDelete, hasBlocks},
		{"/orig/sub1/file1.txt", isDelete, hasBlocks},
	} {
		event := <-ch
		err = checkEvent(event, userName+exp.name, exp.isDelete, exp.hasBlocks)
		if err != nil {
			t.Errorf("%d: %s", i, err)
		}
	}

	// No further events.
	select {
	case event := <-ch:
		t.Errorf("Expected no event, got %v", event)
	case <-time.After(10 * time.Millisecond):
		// Ok. Nothing should ever show up.
	}
}

func TestWatchNew(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, config)

	// Get a watcher for the current subtree, rooted at orig/sub1.
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), upspin.WatchNew, done)
	if err != nil {
		t.Fatal(err)
	}

	// Make further modifications.
	_, err = tree.Put(newDirEntry("/orig/sub1/thesis.pdf", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// The Watcher only gives us new updates.
	for i, exp := range []struct {
		name      upspin.PathName
		isDelete  bool
		hasBlocks bool
	}{
		{"/orig/sub1/thesis.pdf", !isDelete, hasBlocks},
		{"/orig/sub1/file1.txt", isDelete, hasBlocks},
	} {
		event := <-ch
		err = checkEvent(event, userName+exp.name, exp.isDelete, exp.hasBlocks)
		if err != nil {
			t.Errorf("%d: %s", i, err)
		}
	}

	// No further events.
	select {
	case event := <-ch:
		t.Errorf("Expected no event, got %v", event)
	case <-time.After(10 * time.Millisecond):
		// Ok. Nothing should ever show up.
	}
}

func TestWatchNonExistingNode(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// A root must exist for a watcher.
	_, err = tree.Put(newDirEntry("/", isDir, config))
	if err != nil {
		t.Fatal(err)
	}

	// Get a watcher for the current subtree, rooted at orig/sub1.
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), upspin.WatchCurrent, done)
	if err != nil {
		t.Fatal(err)
	}

	// Create a tree.

	// Does not generate an event.
	_, err = tree.Put(newDirEntry("/orig", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	// Does not generate an event.
	_, err = tree.Put(newDirEntry("/orig/sub11", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Put(newDirEntry("/orig/sub1", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Put(newDirEntry("/orig/sub1/somefile.txt", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	// Delete sub1 and re-create it. All generate an event except where
	// marked.
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1/somefile.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1"))
	if err != nil {
		t.Fatal(err)
	}
	// Does not generate an event.
	_, err = tree.Put(newDirEntry("/orig/somecrap", !isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Put(newDirEntry("/orig/sub1", isDir, config))
	if err != nil {
		t.Fatal(err)
	}
	// We get notified of all deletions and the creation of sub1 again, but
	// not the creation of a file not being watched.
	for i, exp := range []struct {
		name      upspin.PathName
		isDelete  bool
		hasBlocks bool
	}{
		{"/orig/sub1", !isDelete, !hasBlocks},
		{"/orig/sub1/somefile.txt", !isDelete, hasBlocks},
		{"/orig/sub1/somefile.txt", isDelete, hasBlocks},
		{"/orig/sub1", isDelete, !hasBlocks},
		{"/orig/sub1", !isDelete, !hasBlocks},
	} {
		event := <-ch
		err = checkEvent(event, userName+exp.name, exp.isDelete, exp.hasBlocks)
		if err != nil {
			t.Errorf("%d: %s", i, err)
		}
	}
}

func TestCannotWatchNonExistentRoot(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}
	// Get a watcher for the current subtree, rooted at orig/sub1.
	done := make(chan struct{})
	_, err = tree.Watch(mkpath(t, userName+"/orig/sub1"), upspin.WatchCurrent, done)
	if !errors.Is(errors.NotExist, err) {
		t.Fatalf("Expected NotExist, got = %v", err)
	}
}

func TestClosingTreeTerminatesWatcher(t *testing.T) {
	config, user := newConfigForTesting(t, userName)
	tree, err := New(config, user)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, config)

	// Get a watcher at the root
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/"), upspin.WatchNew, done)
	if err != nil {
		t.Fatal(err)
	}

	// Find the watcher internally.
	ws := tree.watchers[upspin.PathName(userName+"/")]
	if l := len(ws); l != 1 {
		t.Fatalf("Expected exactly one watcher, got %d", l)
	}
	w := ws[0]

	tree.Close()

	// Wait for watcher to close itself.
	select {
	case <-ch:
		// Ok
	case <-time.After(time.Minute):
		// Don't wait forever or test will abort without a helpful error message.
		t.Error("Watcher did not close fast enough")
	}
	if !w.isClosed() {
		t.Fatal("Watcher did not close")
	}
}

func checkEvent(e *upspin.Event, expectedName upspin.PathName, expectDelete bool, expectBlocks bool) error {
	if e == nil {
		return errors.Str("nil event")
	}
	if e.Entry == nil {
		return errors.Str("nil event entry")
	}
	if e.Entry.SignedName != expectedName {
		return errors.Errorf("SignedName = %s, want = %s", e.Entry.SignedName, expectedName)
	}
	if e.Delete {
		if !expectDelete {
			return errors.Errorf("got Delete event, expected not Delete")
		}
	} else if expectDelete {
		return errors.Errorf("got not Delete event, expected Delete")
	}
	if len(e.Entry.Blocks) == 0 {
		if expectBlocks {
			return errors.Errorf("got zero blocks, expected non-zero")
		}
	} else if !expectBlocks {
		return errors.Errorf("got blocks, expected zero")
	}
	return nil
}
