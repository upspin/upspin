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

	done := r.DirWatch(base, -1)
	if r.Match(upspin.ErrNotSupported) {
		t.Logf("Watch not supported for this DirServer.")
		return
	}
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(file, hasBlocks) {
		t.Fatal(r.Diag())
	}

	// Put an Access file; watch it appear on the channel.
	r.Put(access, accessContent)
	if !r.GotEvent(access, hasBlocks) {
		t.Fatal(r.Diag())
	}
	close(done)

	// Reader can set a watcher, but will get no data due to lack of rights.
	r.As(readerName)
	done = r.DirWatch(base, -1)
	if !r.GotErrorEvent(errors.E(errors.Str("event channel timed out"))) {
		t.Fatal(r.Diag())
	}
	close(done)

	// Allow reader to list, but not read.
	r.As(ownerName)
	r.Put(access, "l: "+readerName+"\n*:"+ownerName)

	r.As(readerName)
	done = r.DirWatch(base, -1)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(base, !hasBlocks) {
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(access, !hasBlocks) { // No blocks returned!
		t.Fatal(r.Diag())
	}
	if !r.GotEvent(file, !hasBlocks) { // No blocks returned!
		t.Fatal(r.Diag())
	}
	close(done)
	e, ok := <-r.Events
	if ok {
		t.Fatalf("Channel had more events: %v", e)
	}
}

// Test some error conditions.

func testWatchErrors(t *testing.T, r *testenv.Runner) {
	const (
		base = ownerName + "/watch-errors"
		file = base + "/aFile"
	)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(file, "dummy")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(base, 3)
	if r.Match(upspin.ErrNotSupported) {
		t.Logf("Watch not supported in this DirServer")
		return
	}
	if !r.GotErrorEvent(errors.E(errors.IO)) {
		t.Fatal(r.Diag())
	}
}
