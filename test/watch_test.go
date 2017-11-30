// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

// This file tests the DirServer Watch API. It only works on implementations
// that support Watch; on others it simply skips this test.

import (
	"testing"

	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

// watchSupported checks for an error after a call to Watch, and if
// there is an ErrNotSupported error, returns false. It returns true
// if there was no error; otherwise it fatals.
func watchSupported(t *testing.T, r *testenv.Runner) bool {
	t.Helper()
	supported, err := watchNotSupportedError(t, r)
	if err != nil {
		t.Fatal(err)
	}
	return supported
}

// watchSupported checks for an error after a call to Watch, and if
// there is an ErrNotSupported error, returns false. It returns true
// if there was no error or the error is not ErrNotSupported.
func watchNotSupportedError(t *testing.T, r *testenv.Runner) (bool, error) {
	err := r.Err()
	if errors.Match(upspin.ErrNotSupported, err) {
		t.Log("Watch not supported for this DirServer.")
		return false, nil
	}
	return true, err
}

func watchEventsValid(t *testing.T, r *testenv.Runner) {
	t.Helper()
	for i, event := range r.Events {
		if event.Error != nil {
			continue
		}
		entry := event.Entry
		if entry == nil {
			t.Fatalf("nil entry in event %d", i)
		}
	}
}

func testWatchCurrent(t *testing.T, r *testenv.Runner) {
	const (
		hasBlocks     = true
		base          = ownerName + "/watch-test"
		file          = base + "/testfile"
		access        = base + "/Access"
		accessContent = "*: " + ownerName
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(file, "something")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	done := r.DirWatch(base, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}
	r.GetNEvents(2)
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(file, hasBlocks) {
		t.Fatal(r.Diag())
	}

	watchEventsValid(t, r)

	// Put an Access file; watch it appear on the channel.
	r.Put(access, accessContent)
	r.GetNEvents(2)
	if !r.GotEvent(access, hasBlocks) {
		t.Fatal(r.Diag())
	}
	close(done)

	// Reader can set a watcher, but will get no data due to lack of rights.
	r.As(readerName)
	done = r.DirWatch(base, upspin.WatchCurrent)
	if !r.GetErrorEvent(errors.E("no response on event channel after one second")) {
		t.Fatal(r.Diag())
	}
	close(done)

	// Allow reader to list, but not read.
	r.As(ownerName)
	r.Put(access, "l: "+readerName+"\n*:"+ownerName)

	r.As(readerName)
	done = r.DirWatch(base, upspin.WatchCurrent)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.GetNEvents(3)
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(access, hasBlocks) {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(file, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	close(done)
	if r.GetNEvents(1) {
		t.Fatalf("Channel had more events")
	}
	watchEventsValid(t, r)
}

// Test some error conditions.

func testWatchErrors(t *testing.T, r *testenv.Runner) {
	const (
		base    = ownerName + "/watch-errors"
		file    = base + "/aFile"
		badFile = "nobody@x/foo"
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(file, "dummy")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(base, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}

	// Should get an error for bad file syntax
	r.DirWatch(badFile, 777)
	if !r.Failed() {
		t.Fatalf("expected Watch error for bad file name %q", badFile)
	}

	// 777777 is an implausible sequence number, at least in this test.
	// TODO: Find a better way to test this.
	r.DirWatch(base, 777777)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if !r.GetErrorEvent(errors.E(errors.Invalid)) {
		t.Fatal(r.Diag())
	}
}

func testWatchNonExistentFile(t *testing.T, r *testenv.Runner) {
	const (
		hasBlocks = true
		base      = ownerName + "/watch-non-existent-file"
		file      = base + "/aFile"
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	// Don't create the file yet.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(base, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}

	r.GetNEvents(1)
	// Should see the directory.
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}

	// Now create the file. Should see it appear.
	r.Put(file, "something")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.GetNEvents(2)
	if !r.GotEvent(file, hasBlocks) {
		t.Fatal(r.Diag())
	}
	watchEventsValid(t, r)
}

func testWatchNonExistentDir(t *testing.T, r *testenv.Runner) {
	const (
		hasBlocks = true
		base      = ownerName + "/watch-non-existent-dir"
		file      = base + "/aFile"
	)

	r.As(ownerName)
	// Don't create the dir yet.

	r.DirWatch(base, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}

	// Now create the dir. Should see it appear.
	r.MakeDirectory(base)
	// Don't create the file yet.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Should see the directory.
	r.GetNEvents(1)
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	// Now create the file. Should see it appear.
	r.Put(file, "something")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.GetNEvents(2)
	if !r.GotEvent(file, hasBlocks) {
		t.Fatal(r.Diag())
	}
	watchEventsValid(t, r)
}

func testWatchForbiddenFile(t *testing.T, r *testenv.Runner) {
	const (
		hasBlocks              = true
		base                   = ownerName + "/watch-forbidden-file"
		file                   = base + "/aFile"
		access                 = base + "/Access"
		forbiddenAccessContent = "*: " + ownerName
		allowedAccessContent   = "*: " + ownerName + " " + readerName
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(access, forbiddenAccessContent)
	r.Put(file, "something")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Switch users. Should not see event.
	r.As(readerName)
	r.DirWatch(file, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}
	r.GetNEvents(1)
	if r.GotEvent(file, hasBlocks) {
		t.Fatal("Should not see event for forbidden file")
	}

	// Now grant permission.
	r.As(ownerName)
	r.Put(access, allowedAccessContent)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Now should see file as other user.
	r.As(readerName)
	r.DirWatch(file, upspin.WatchCurrent)
	r.GetNEvents(1)
	if !r.GotEvent(file, hasBlocks) {
		t.Fatal(r.Diag())
	}
	watchEventsValid(t, r)
}

func testWatchSubtree(t *testing.T, r *testenv.Runner) {
	const (
		hasBlocks = true
		base      = ownerName + "/watch-subtree"
		file      = base + "/aFile"
		dir       = base + "/dir"
		dirFile   = dir + "/file"
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(dir, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}

	// Create file in root. Should not see event.
	r.Put(file, "something")
	r.GetNEvents(1)
	if r.GotEvent(file, hasBlocks) {
		t.Fatal("Should not see event for parent directory")
	}

	// Create file in subdir. Should see event.
	r.Put(dirFile, "something")
	r.GetNEvents(2)
	if !r.GotEvent(dirFile, hasBlocks) {
		t.Fatal(r.Diag())
	}
	watchEventsValid(t, r)
}

func testWatchFile(t *testing.T, r *testenv.Runner) {
	const (
		base      = ownerName + "/watch-file"
		file      = base + "/aFile"
		hasBlocks = true
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(file, upspin.WatchCurrent)
	if !watchSupported(t, r) {
		return
	}

	// Do it all twice to check it works after deletion.
	for i := 0; i < 2; i++ {
		// Create file. It doesn't exist yet. Should see event.
		r.Put(file, "something")
		r.GetNEvents(1)
		if !r.GotEvent(file, hasBlocks) {
			t.Fatal(r.Diag())
		}
		watchEventsValid(t, r)

		// Modify file. Should see event.
		r.Put(file, "something else")
		r.GetNEvents(1)
		if !r.GotEvent(file, hasBlocks) {
			t.Fatal(r.Diag())
		}
		watchEventsValid(t, r)

		// Modify file again. Should see event. (This didn't work at one point.)
		r.Put(file, "something else again")
		r.GetNEvents(1)
		if !r.GotEvent(file, hasBlocks) {
			t.Fatal(r.Diag())
		}
		watchEventsValid(t, r)

		// Delete file. Should see event.
		r.Delete(file)
		r.GetDeleteEvent(file)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
	}
}

func testWatchNonExistentRoot(t *testing.T, r *testenv.Runner) {
	r.As(ownerName)
	r.DirWatch(readerName+"/", upspin.WatchCurrent)
	supported, err := watchNotSupportedError(t, r)
	if !supported {
		return
	}
	if !errors.Is(errors.NotExist, err) {
		t.Fatalf("Expected %v, got %v", errNotExist, err)
	}
}
