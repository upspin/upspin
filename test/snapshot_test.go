// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

func testSnapshot(t *testing.T, r *testenv.Runner) {
	const (
		base        = ownerName + "/snapshot-test"
		dir         = base + "/dir"
		file        = dir + "/file"
		accessFile  = base + "/Access"
		access      = "*:all" // intentionally permissive.
		snapshotDir = snapshotUser + "/"

		// must be in sync with dir/server/snapshot.go
		snapshotControlFile = snapshotDir + "/TakeSnapshot"
	)

	data := randomString(t, 16)

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(accessFile, access)
	r.MakeDirectory(dir)
	r.Put(file, data)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Take the snapshot.
	r.As(snapshotUser)
	r.MakeDirectory(snapshotDir)
	if err := r.Err(); err != nil && !errors.Is(errors.Exist, err) {
		// It's OK for the snapshot directory to exist already,
		// as it won't be deleted after previous test runs.
		t.Fatal(err)
	}
	r.Put(snapshotControlFile, "")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// If the control file actually exists in snapshotDir, then this
	// DirServer does not support snapshotting.
	r.Get(snapshotControlFile)
	if !r.Failed() {
		if r.Data == "" {
			t.Skip("Snapshotting not supported.")
		}
		t.Fatalf("Non-empty snapshot control file: %q.", r.Data)
	}

	// Verify snapshot was taken today and has the correct data in it.
	r.As(ownerName)
	snapPattern := snapshotDir + time.Now().UTC().Format("2006/01/02") + "/*"

	// Use repeated Globs to find the snapshot; we can't use Watch because
	// it is not supported for snapshots. See issue #536.
	var snapshot upspin.PathName
	for i := 0; i < 100; i++ {
		r.Glob(snapPattern)
		err := r.Err()
		if err == nil && len(r.Entries) > 0 {
			snapshot = r.Entries[0].Name
			break
		}
		if err != nil && !errors.Is(errors.NotExist, err) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if snapshot == "" {
		t.Fatalf("timed out waiting for snapshot in %q", snapPattern)
	}
	fileInSnapshot := path.Join(snapshot, "snapshot-test", "dir", "file")

	// Ensure no one else can read this snapshotted file, even with a
	// permissive Access file.
	r.As(readerName)
	r.DirLookup(fileInSnapshot)
	if !r.Match(errors.E(errors.Private)) {
		t.Fatal(r.Diag())
	}

	// WhichAccess for a snapshotted name returns nothing, even if the
	// Access file exists in the path, which is the case here.
	r.As(ownerName)
	r.DirWhichAccess(fileInSnapshot)
	if !r.GotNilEntry() {
		t.Fatal(r.Diag())
	}

	// No one can delete snapshots.
	r.Delete(fileInSnapshot)
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}

	// No one can overwrite a snapshot.
	r.Put(fileInSnapshot, "yo")
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}
}

func randomString(t *testing.T, size int) string {
	buf := make([]byte, size)
	_, err := rand.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", buf)
}
