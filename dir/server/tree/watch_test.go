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

func TestWatchFromBeginning(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t, userName)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, _ := mkdir(t, tree, context, "/")
	t.Logf("===p: %s", p.Path())

	ch, err := tree.Watch(p, 0, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}

	// Put something under the root and observe a notification.
	dirPath, dir := mkdir(t, tree, context, "/dir")

	event := <-ch
	err = checkEvent(event, dir.SignedName, !isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Put something under dir and observe another notification.
	subdirPath, subdir := mkdir(t, tree, context, "/dir/subdir")

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
	ch2, err := tree.Watch(dirPath, 0, done)
	if err != nil {
		t.Fatal(err)
	}

	// Put a file under dir. Watch two updates, one on each channel.
	p, entry := newDirEntry("/dir/fileA.txt", !isDir, context)
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

	p, entry = newDirEntry("/dir/fileB.txt", !isDir, context)
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
	context, log, logIndex := newConfigForTesting(t, userName)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, context)

	// Generate a delete event.
	_, err = tree.Delete(mkpath(t, userName+"/orig/sub1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Watch for events that happened from a specific log order and on,
	// for a subdirectory. The magic number below (163) is the log offset
	// right after "mkdir /orig/sub2/".
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), 163, done)
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

func TestWatchFromCurrent(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t, userName)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, context)

	// Get a watcher for the current subtree, rooted at orig/sub1.
	done := make(chan struct{})
	ch, err := tree.Watch(mkpath(t, userName+"/orig/sub1"), -1, done)
	if err != nil {
		t.Fatal(err)
	}

	// Make further modifications.
	_, err = tree.Put(newDirEntry("/orig/sub1/thesis.pdf", !isDir, context))
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

// Tests internal functionality that can be tricky.
func TestRemoveDeadWatchers(t *testing.T) {
	d := make(chan struct{})
	close(d)
	done := &watcher{
		events: make(chan *upspin.Event),
		done:   d,
		closed: 1,
	}

	open := &watcher{
		events: make(chan *upspin.Event),
		done:   make(chan struct{}),
		closed: 0,
	}

	for i, tc := range []struct {
		watchers []*watcher
		open     int
	}{
		{[]*watcher{}, 0},
		{[]*watcher{open}, 1},
		{[]*watcher{done}, 0},
		{[]*watcher{open, open}, 2},
		{[]*watcher{open, done}, 1},
		{[]*watcher{done, open}, 1},
		{[]*watcher{done, done}, 0},
		{[]*watcher{done, open, done}, 1},
		{[]*watcher{open, done, done}, 1},
		{[]*watcher{open, open, open}, 3},
		{[]*watcher{done, done, done}, 0},
		{[]*watcher{open, done, done, open, done}, 2},
	} {
		n := &node{
			watchers: tc.watchers,
		}
		removeDeadWatchers(n)

		// Verify that only the expected number of open watchers remain.
		if got, want := len(n.watchers), tc.open; got != want {
			t.Fatalf("%d: open = %d, want = %d", i, got, want)
		}
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
