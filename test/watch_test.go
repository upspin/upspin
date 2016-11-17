// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

// This file tests the DirServer Watch API. It only works for the dir/server for
// now since inprocess does not (yet?) support it.

import (
	"testing"

	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

func TestWatchCurrent(t *testing.T) {
	ownerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: ownerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	readerEnv, err := testenv.New(&testenv.Setup{
		OwnerName: readerName,
		Packing:   upspin.PlainPack,
		Kind:      "server",
		Cleanup:   cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}

	const (
		hasBlocks     = true
		base          = ownerName + "/watch-test"
		file          = base + "/testfile"
		access        = base + "/Access"
		accessContent = "*: " + ownerName
	)

	r := testenv.NewRunner()
	r.AddUser(ownerEnv.Context)
	r.AddUser(readerEnv.Context)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(file, "something")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.DirWatch(base, -1)
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

	// Reader does not have rights to set a watcher.
	r.As(readerName)
	r.DirWatch(base, -1)
	if !r.Match(errors.E(errors.Private, upspin.PathName(base))) {
		t.Fatal(r.Diag())
	}

	// Allow reader to list, but not read.
	r.As(ownerName)
	r.Put(access, "l: "+readerName+"\n*:"+ownerName)

	r.As(readerName)
	done := r.DirWatch(base, -1)
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
		t.Fatal("Had more events: %v", e)
	}
}
