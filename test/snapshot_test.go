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
	r.Put(snapshotControlFile, "")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// If the control file actually exists in snapshotDir, then this
	// DirServer does not support snapshotting.
	r.Get(snapshotControlFile)
	if !r.Failed() {
		if r.Data == "" {
			t.Log("Snapshotting not supported.")
			return
		}
		t.Fatalf("Non-empty snapshot control file: %q.", r.Data)
	}
	t.Log("Snapshot command issued")

	// Verify snapshot was taken today and has the correct data in it.
	r.As(ownerName)
	snapPattern := snapshotDir + time.Now().UTC().Format("2006/01/02") + "/"

	// Watch the snapshot root for the new snapshot.
	done := r.DirWatch(upspin.PathName(snapPattern), -1)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// We need should see two entries: the top directory
	// with the date, and the subdirectory with the time.
	var found upspin.PathName
	for i := 0; i < 2; i++ {
		// We use GetNEvents because we don't have a fixed name to use
		// with r.GotEvent(name).
		if !r.GetNEvents(1) {
			t.Fatal(r.Diag())
		}
		entry := r.Events[0].Entry

		// Check  entry contents and name.
		file := path.Join(entry.Name, "snapshot-test", "dir", "file")
		r.Get(file)
		if r.Failed() {
			// TODO: remove once the failure is fixed.
			t.Logf("Failed to Get %q. Attempting to Glob %q", file, entry.Name)
			debugFailure(t, r, entry.Name, file)

			// t.Fatal(r.Diag())
		}
		if r.Data == data {
			found = file
			break
		}
	}
	close(done)
	if found == "" {
		t.Fatalf("Unable to find a snapshot in %s", snapPattern)
	}

	// Ensure no one else can read this snapshotted file, even with a
	// permissive Access file.
	r.As(readerName)
	r.DirLookup(found)
	if !r.Match(errors.E(errors.Private)) {
		t.Fatal(r.Diag())
	}

	// WhichAccess for a snapshotted name returns nothing, even if the
	// Access file exists in the path, which is the case here.
	r.As(ownerName)
	r.DirWhichAccess(found)
	if !r.GotNilEntry() {
		t.Fatal(r.Diag())
	}

	// No one can delete snapshots.
	r.Delete(found)
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}

	// No one can overwrite a snapshot.
	r.Put(found, "yo")
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}
}

// TODO: remove.
func debugFailure(t *testing.T, r *testenv.Runner, dir, file upspin.PathName) {
	c := r.ClientFor(ownerName)

	entries, err := c.Glob(string(dir) + "/*")
	if err != nil {
		t.Fatalf("Error Globbing: %v", err)
	}
	for i, e := range entries {
		t.Logf("%d: %s", i, e.Name)
	}

	e, err := c.Lookup(file, true)
	if err != nil {
		t.Logf("Error in lookup of %q: %v", file, err)
	}
	t.Logf("Entry: %+v", e)
}

func randomString(t *testing.T, size int) string {
	buf := make([]byte, size)
	_, err := rand.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", buf)
}
