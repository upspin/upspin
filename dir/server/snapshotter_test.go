// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strings"
	"testing"

	"upspin.io/path"
	"upspin.io/upspin"
)

func TestCreateNewDir(t *testing.T) {
	s := newDirServerForTesting(t, userName)

	name := upspin.PathName(userName + "/snapshot/2016/05/09")
	entry, err := s.makeSnapshotPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry.Name, name; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}

	// Try to make it again.
	entry, err = s.makeSnapshotPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry.Name, name+".1"; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}
}

func TestSnapshot(t *testing.T) {
	s := newDirServerForTesting(t, userName)

	// Forcefully take a few snapshots.
	for i := 0; i < 5; i++ {
		err := s.takeSnapshot()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Glob inside all the snapshots.
	entries, err := s.Glob(userName + "/" + path.SnapshotPrefix + "/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	expectedEntries := []upspin.PathName{
		"Access",
		"snapshot",
		"some_new_file.txt",
	}
	for i, e := range entries {
		if i >= 2 { // because the first two are from the test above.
			t.Logf("Globbing: %q", string(e.Name)+"/*")
			ssEntries, err := s.Glob(string(e.Name) + "/*")
			if err != nil {
				t.Fatal(err)
			}
			if len(ssEntries) != len(expectedEntries) {
				t.Fatalf("len(ssEntries) = %d, want = %d", len(ssEntries), len(expectedEntries))
			}
			for j, ne := range ssEntries {
				if !strings.HasSuffix(string(ne.Name), string(expectedEntries[j])) {
					t.Errorf("ne.Name = %q, want suffix = %q", ne.Name, expectedEntries[j])
				}
			}
		}
	}
}
