// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import "testing"

const (
	isDelete  = true
	hasBlocks = true
)

func TestCurrent(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t, userName)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	p, _ := mkdir(t, tree, context, "/")

	ch, err := tree.Watch(p, 0, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}

	// Put something under the root and observe a notification.
	dirPath, dir := mkdir(t, tree, context, "/dir")

	event := <-ch
	err = checkEvent(event, dir, !isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Put something under dir and observe another notification.
	subdirPath, subdir := mkdir(t, tree, context, "/dir/subdir")

	event = <-ch
	err = checkEvent(event, subdir, !isDelete, !hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Delete an entry and observe the notification.
	_, err = tree.Delete(subdirPath)
	if err != nil {
		t.Fatal(err)
	}

	event = <-ch
	err = checkEvent(event, subdir, isDelete, !hasBlocks)
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
	err = checkEvent(event, entry, !isDelete, hasBlocks)
	if err != nil {
		t.Error(err)
	}

	event = <-ch2
	err = checkEvent(event, entry, !isDelete, hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Stop watching ch2 and trigger another notification. This time only
	// one event is sent.
	close(done)
	p, entry = newDirEntry("/dir/fileB.txt", !isDir, context)
	_, err = tree.Put(p, entry)
	if err != nil {
		t.Fatal(err)
	}

	// First channel gets it.
	event = <-ch
	err = checkEvent(event, entry, !isDelete, hasBlocks)
	if err != nil {
		t.Error(err)
	}

	// Second channel is now closed.
	event, ok := <-ch2
	if ok || event != nil {
		t.Errorf("Expected channel closed, got %v", event)
	}
}

func TestFromOrder(t *testing.T) {
	context, log, logIndex := newConfigForTesting(t, userName)
	tree, err := New(context, log, logIndex)
	if err != nil {
		t.Fatal(err)
	}

	buildTree(t, tree, context)

	// As for events that happened from the middle on and only for a sub
	// directory.

	done := make(chan struct{})
	_, err = tree.Watch(mkpath(t, userName+"/orig/sub1"), 32, done)
	if err != nil {
		t.Fatal(err)
	}
}
