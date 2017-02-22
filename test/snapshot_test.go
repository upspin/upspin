// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"strings"
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
		access      = "*:all"
		snapshotDir = snapshotUser + "/"
		// must be in sync with dir/server/snapshot.go
		snapshotControlFile = snapshotDir + "/TakeSnapshot"
	)

	data := uniqueData(t, 16) // makes 16 bytes of random data.

	r.As(ownerName)
	r.MakeDirectory(base)
	r.Put(accessFile, access)
	r.MakeDirectory(dir)
	r.Put(file, data)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

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
		t.Fatalf("Non-empty snapshot control file: %q. Something is wrong.", r.Data)
	}

	time.Sleep(1 * time.Second)
	r.As(ownerName)

	// Verify snapshot was taken today and has the correct data in it.
	snapPattern := snapshotDir + time.Now().UTC().Format("2006/01/02") + "/"

	// Watch the snapshot root for the new snapshot. There could be more
	// than one entry, so we try a few times.
	found := false
	t.Logf("=== snapPattern: %s", snapPattern)
	done := r.DirWatch(upspin.PathName(snapPattern), -1)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	for i := 0; i < 10; i++ {
		if !r.GetNEvents(1) {
			t.Fatal(r.Diag())
		}
		entry := r.Events[len(r.Events)-1].Entry
		// See if this entry has the name and contents we're looking
		// for.
		if strings.HasSuffix(string(entry.Name), "dir/file") {
			// Attempt to get the contents of the file.
			r.Get(entry.Name)
			if r.Failed() {
				t.Fatal(r.Diag())
			}
			if r.Data == data {
				found = true
				break
			}
		}
	}
	close(done)
	if !found {
		t.Fatalf("Unable to find a snapshot in %s", snapPattern)
	}

	// Attempt to read our unique file in one of the snapshots (there should
	// be only one when running tests locally with -short, but tests using
	// the test server may have multiple ones).
	for _, dir := range r.Entries {
		snapFile := path.Join(dir.Name, "dir", "file")
		r.Get(snapFile)
		if r.Data == data {
			found = true
			break
		}
	}

	// Ensure no one else can read this snapshotted file, even with a
	// permissive Access file.
}

func uniqueData(t *testing.T, size int) string {
	buf := make([]byte, size)
	_, err := rand.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", buf)
}
