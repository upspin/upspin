// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strings"
	"testing"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	userFirstName = "bambi"
	domain        = "forest.earth"
	canonicalUser = userFirstName + "@" + domain
	snapshotUser  = userFirstName + "+snapshot@" + domain

	isDir = true
)

var defaultEnt = upspin.DirEntry{
	Attr:     upspin.AttrNone,
	Writer:   canonicalUser,
	Sequence: upspin.SeqNotExist,
	Packing:  upspin.PlainPack,
}

func TestSnapshot(t *testing.T) {
	s := newDirServerForTesting(t, canonicalUser)
	create(t, s, canonicalUser+"/", isDir)
	create(t, s, canonicalUser+"/dir", isDir)
	create(t, s, canonicalUser+"/file.pdf", !isDir)

	snap := newDirServerForTesting(t, snapshotUser)
	create(t, snap, snapshotUser+"/", isDir)

	// Nothing exists under snapshotUser yet.
	ents, err := snap.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > 0 {
		t.Fatalf("got = %d entries, want = 0", len(ents))
	}

	// Force a snapshot for all users who have a +snapshot tree.
	err = snap.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}

	// Verify there are items under the snapshot user now.
	ents, err = snap.Glob(snapshotUser + "/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// And one entry was created.
	if len(ents) != 1 {
		t.Fatalf("got = %d entries, want = 1", len(ents))
	}
	// Inside the snapshot directory, there's the entire root of userName.
	// Check that everything is there.
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		prefix string
		suffix string
	}{
		{snapshotUser + "/", "/dir"},
		{snapshotUser + "/", "/file.pdf"},
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if !strings.HasPrefix(string(e.Name), expected[i].prefix) {
			t.Errorf("%d: e.Name = %q, want prefix %q", i, e.Name, expected[i].prefix)
		}
		if !strings.HasSuffix(string(e.Name), expected[i].suffix) {
			t.Errorf("%d: e.Name = %q, want suffix %q", i, e.Name, expected[i].suffix)
		}
	}

	// Snapshot again and nothing happens, because the previous snapshot is
	// recent enough.
	err = snap.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}
	// Only one entry still.
	ents, err = snap.Glob(snapshotUser + "/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("got = %d entries, want = 1", len(ents))
	}
}

func TestForceSnapshotVersioning(t *testing.T) {
	mockTime := upspin.Now()
	s := newDirServerForTesting(t, snapshotUser)
	s.now = func() upspin.Time {
		return mockTime
	}

	ents, err := s.Glob(snapshotUser + "/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// A pre-existing entry exists.
	if len(ents) != 1 {
		t.Fatalf("got = %d entries, want = 1", len(ents))
	}
	// Re-use the same destination name, so we force the creation of a new
	// version.
	dstDir := ents[0].Name
	dstPath, err := path.Parse(dstDir)
	if err != nil {
		t.Fatal(err)
	}

	mockTime++ // Pretend one second has elapsed (our time resolution).

	// Force two new snapshots.
	err = s.takeSnapshot(dstPath, canonicalUser+"/")
	if err != nil {
		t.Fatal(err)
	}

	mockTime++ // Another second has elapsed.

	err = s.takeSnapshot(dstPath, canonicalUser+"/")
	if err != nil {
		t.Fatal(err)
	}

	// The tree now contains three snapshotted versions.
	ents, err = s.Glob(snapshotUser + "/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// Two new entries were created.
	if len(ents) != 3 {
		t.Fatalf("got = %d entries, want = 3", len(ents))
	}
	// Verify the last element of the second entry contains a ".0" appended
	// to it.
	if !strings.HasSuffix(string(ents[2].Name), ".2") {
		t.Errorf("got %q, want suffix '.2'", ents[1].Name)
	}

	// Assert the newly-created name contains only one ".version" number.
	p, _ := path.Parse(ents[1].Name) // path is known valid.
	if got, want := strings.Count(p.FilePath(), "."), 1; got != want {
		t.Errorf("num .version = %d, want = %d: %s", got, want, p.FilePath())
	}
	p, _ = path.Parse(ents[2].Name) // path is known valid.
	if got, want := strings.Count(p.FilePath(), "."), 1; got != want {
		t.Errorf("num .version = %d, want = %d: %s", got, want, p.FilePath())
	}
	// Assert times are monotonically increasing.
	if ents[1].Time >= ents[2].Time {
		t.Errorf("time = %d, want < %d", ents[1].Time, ents[2].Time)
	}
	if ents[0].Time >= ents[1].Time {
		t.Errorf("time = %d, want < %d", ents[0].Time, ents[1].Time)
	}
}

func TestOnlyOwnerCanLookup(t *testing.T) {
	// snapshotUser can Lookup.
	s := newDirServerForTesting(t, snapshotUser)
	_, err := s.Lookup(snapshotUser + "/")
	if err != nil {
		t.Fatal(err)
	}

	// owner of snapshot can Lookup.
	s = newDirServerForTesting(t, canonicalUser)
	_, err = s.Lookup(snapshotUser + "/")
	if err != nil {
		t.Fatal(err)
	}

	// no one else can.
	s = newDirServerForTesting(t, "spy@nsa.gov")
	_, err = s.Lookup(snapshotUser + "/")
	expectedErr := errors.E(upspin.PathName(snapshotUser+"/"), errNotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestOnlyOwnerCanGlob(t *testing.T) {
	// snapshotUser can Glob.
	s := newDirServerForTesting(t, snapshotUser)
	_, err := s.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}

	// owner of snapshot can Glob.
	s = newDirServerForTesting(t, canonicalUser)
	_, err = s.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}

	// no one else can.
	s = newDirServerForTesting(t, "spy@nsa.gov")
	_, err = s.Glob(snapshotUser + "/*")
	expectedErr := errors.E(upspin.PathName(snapshotUser+"/*"), errNotExist)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestSnapshotIsReadOnly(t *testing.T) {
	for _, c := range []struct {
		user upspin.UserName
		err  error
	}{
		{snapshotUser, errReadOnly},
		{canonicalUser, errReadOnly},
		{"spy@kgb.ru", errNotExist},
	} {
		s := newDirServerForTesting(t, c.user)

		// Ensures no user can:

		// 1) Delete a snapshot;
		_, err := s.Delete(snapshotUser + "/foo")
		if !errors.Match(c.err, err) {
			t.Errorf("%s: err = %v, want = %v", c.user, err, errReadOnly)
		}

		// 2) Create a directory in the snapshot tree;
		de := &upspin.DirEntry{
			Name:       snapshotUser + "/bla",
			SignedName: snapshotUser + "/bla",
			Writer:     c.user,
			Attr:       upspin.AttrDirectory,
		}
		_, err = s.Put(de)
		if !errors.Match(c.err, err) {
			t.Errorf("%s: err = %v, want = %v", c.user, err, errReadOnly)
		}

		// 3) Modify a file in the snapshot.
		de.Attr = upspin.AttrNone
		_, err = s.Put(de)
		if !errors.Match(c.err, err) {
			t.Errorf("%s: err = %v, want = %v", c.user, err, errReadOnly)
		}
	}
}

func create(t *testing.T, s *server, name upspin.PathName, isDir bool) {
	var err error
	if isDir {
		var p path.Parsed
		p, err = path.Parse(name)
		if err != nil {
			t.Fatal(err)
		}
		mu := userLock(p.User())
		mu.Lock()
		err = s.mkDirIfNotExist(p)
		mu.Unlock()
	} else {
		entry := defaultEnt
		entry.Name = name
		entry.SignedName = name
		_, err = s.Put(&entry)
	}
	if err != nil {
		t.Fatal(err)
	}
}
