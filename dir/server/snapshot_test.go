// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"upspin.io/access"
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
	dir := generatorInstance.(*server)
	s, _ := newDirServerForTesting(t, canonicalUser)
	snap, _ := newDirServerForTesting(t, snapshotUser)

	create(t, s, canonicalUser+"/", isDir)
	create(t, s, canonicalUser+"/dir", isDir)
	create(t, s, canonicalUser+"/file.pdf", !isDir)

	// Ensures owner's server s can create snapshotUser.
	create(t, s, snapshotUser+"/", isDir)

	// Nothing exists under snapshotUser yet.
	ents, err := snap.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > 0 {
		t.Fatalf("got = %d entries, want = 0", len(ents))
	}

	// Set an arbitrary time close to midnight so we can move the
	// clock within a 24-hour period.
	tm, err := time.Parse(time.RFC3339, "2017-01-02T01:00:00+00:00")
	if err != nil {
		t.Fatal(err)
	}
	mockTime.set(tm)

	// Force a snapshot for all users who have a +snapshot tree.
	err = dir.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}

	// Verify there are items under the snapshot user now.
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// And one entry was created.
	if len(ents) != 1 {
		t.Fatalf("got = %d entries, want = 1", len(ents))
	}
	// Inside the snapshot directory, there's the entire root of userName.
	// Check that everything is there.
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*/*")
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

	mockTime.addSecond(3 * 60 * 60) // Add three hours.

	// Snapshot again and nothing happens, because the previous snapshot is
	// recent enough.
	err = dir.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}
	// Only one entry still.
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("got = %d entries, want = 1", len(ents))
	}

	// add ten hours, for a total of 13 since the previous snapshot.
	mockTime.addSecond(10 * 60 * 60)

	// Run the snapshot loop again.
	err = dir.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}
	// Now two entries should exist.
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("got = %d entries, want = 2", len(ents))
	}

	// Add another three hours. It should not snapshot again yet.
	mockTime.addSecond(3 * 60 * 60)

	err = dir.snapshotAll()
	if err != nil {
		t.Fatal(err)
	}
	ents, err = snap.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("got = %d entries, want = 2", len(ents))
	}
}

func TestForceSnapshotVersioning(t *testing.T) {
	s, _ := newDirServerForTesting(t, snapshotUser)
	ents, err := s.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// Two pre-existing entries exist.
	if len(ents) != 2 {
		t.Fatalf("got = %d entries, want = 2", len(ents))
	}
	// Re-use the same destination name, so we force the creation of a new
	// version.
	dstDir := ents[0].Name
	dstPath, err := path.Parse(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	dstPath = dstPath.Drop(1)

	mockTime.addSecond(60) // Pretend one minute has elapsed (our time resolution).

	// Force two new snapshots.
	err = s.takeSnapshot(dstPath, canonicalUser+"/")
	if err != nil {
		t.Fatal(err)
	}

	mockTime.addSecond(60) // Another minute has elapsed.

	err = s.takeSnapshot(dstPath, canonicalUser+"/")
	if err != nil {
		t.Fatal(err)
	}

	// The tree now contains three snapshotted versions.
	ents, err = s.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// Two new entries were created.
	if len(ents) != 4 {
		t.Fatalf("got = %d entries, want = 4", len(ents))
	}
	// Verify the last element of the second entry is at minute ":01".
	if !strings.HasSuffix(string(ents[2].Name), ":01") {
		t.Errorf("got %q, want suffix ':01'", ents[2].Name)
	}

	// Assert the newly-created name contains only one "hh:mm" entry.
	p, _ := path.Parse(ents[2].Name) // path is known valid.
	if got, want := strings.Count(p.FilePath(), ":"), 1; got != want {
		t.Errorf("num time = %d, want = %d: %s", got, want, p.FilePath())
	}
	p, _ = path.Parse(ents[3].Name) // path is known valid.
	if got, want := strings.Count(p.FilePath(), ":"), 1; got != want {
		t.Errorf("num time = %d, want = %d: %s", got, want, p.FilePath())
	}
}

func TestForceSnapshot(t *testing.T) {
	s, _ := newDirServerForTesting(t, snapshotUser)

	ents, err := s.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// Some pre-existing entries exists.
	preExisting := len(ents)
	if preExisting == 0 {
		t.Fatalf("got = %d pre-existing entries, want > 0", preExisting)
	}

	mockTime.addSecond(60) // A minute has elapsed.

	// Force a snapshot to be taken for canonicalUser.
	err = s.takeSnapshotFor(snapshotUser)
	if err != nil {
		t.Fatal(err)
	}

	ents, err = s.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(ents), preExisting+1; got != want {
		t.Fatalf("got = %d entries, want = %d", got, want)
	}
}

func TestTriggerSnapshotWithPut(t *testing.T) {
	s, _ := newDirServerForTesting(t, snapshotUser)

	ents, err := s.Glob(snapshotUser + "/*/*/*/*")
	if err != nil {
		t.Fatal(err)
	}
	// Some pre-existing entries exists.
	preExisting := len(ents)
	if preExisting == 0 {
		t.Fatalf("got = %d pre-existing entries, want > 0", preExisting)
	}

	mockTime.addSecond(600) // 10 minutes have elapsed.

	// Put a special DirEntry to trigger the background snapshot.
	de := &upspin.DirEntry{
		Name:       snapshotUser + "/" + snapshotControlFile,
		SignedName: snapshotUser + "/" + snapshotControlFile,
		Packing:    upspin.PlainPack,
		Writer:     "some@valid.user.name",
	}

	entry, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, de) {
		t.Fatalf("got = %v, want = %v", entry, de)
	}

	// Poll looking for the extra entry.
	var numEnts int
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		ents, err := s.Glob(snapshotUser + "/*/*/*/*")
		if err != nil {
			t.Fatal(err)
		}
		numEnts = len(ents)
		if numEnts != preExisting {
			break
		}
	}
	if numEnts != preExisting+1 {
		t.Errorf("got = %d entries, want = %d", numEnts, preExisting+1)
	}

	// Check some errors.
	expectedErr := errors.E(errors.Invalid, errors.E(de.Name))

	// Empty blocks.
	de.Packing = upspin.PlainPack
	de.Blocks = []upspin.DirBlock{
		{
			Size: 32,
		},
	}
	_, err = s.Put(de)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
	}

	// Link.
	de.Blocks = nil
	de.Attr = upspin.AttrLink
	_, err = s.Put(de)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
	}

	// Directory.
	de.Attr = upspin.AttrDirectory
	_, err = s.Put(de)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %v, want = %v", err, expectedErr)
	}
}

func TestOnlyOwnerCanLookup(t *testing.T) {
	// snapshotUser can Lookup.
	s, _ := newDirServerForTesting(t, snapshotUser)
	_, err := s.Lookup(snapshotUser + "/")
	if err != nil {
		t.Fatal(err)
	}

	// owner of snapshot can Lookup.
	s, _ = newDirServerForTesting(t, canonicalUser)
	_, err = s.Lookup(snapshotUser + "/")
	if err != nil {
		t.Fatal(err)
	}

	// no one else can.
	s, _ = newDirServerForTesting(t, "spy@nsa.gov")
	_, err = s.Lookup(snapshotUser + "/")
	if !errors.Is(errors.Private, err) {
		t.Fatalf("err = %v, want = %v", err, errPrivate)
	}
}

func TestOnlyOwnerCanGlob(t *testing.T) {
	// snapshotUser can Glob.
	s, _ := newDirServerForTesting(t, snapshotUser)
	_, err := s.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}

	// owner of snapshot can Glob.
	s, _ = newDirServerForTesting(t, canonicalUser)
	_, err = s.Glob(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}

	// no one else can.
	s, _ = newDirServerForTesting(t, "spy@nsa.gov")
	_, err = s.Glob(snapshotUser + "/*")
	expectedErr := errors.E(errNotExist, errors.E(upspin.PathName(snapshotUser+"/")))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestSnapshotIsReadOnly(t *testing.T) {
	for _, c := range []struct {
		user upspin.UserName
		err  error
	}{
		{snapshotUser, access.ErrPermissionDenied},
		{canonicalUser, access.ErrPermissionDenied},
		{"spy@kgb.ru", errPrivate},
	} {
		s, _ := newDirServerForTesting(t, c.user)

		// Ensures no user can:

		// 1) Delete a snapshot;
		_, err := s.Delete(snapshotUser + "/foo")
		if !errors.Match(c.err, err) {
			t.Errorf("%s: err = %v, want = %v", c.user, err, c.err)
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
			t.Errorf("%s: err = %v, want = %v", c.user, err, c.err)
		}

		// 3) Modify a file in the snapshot.
		de.Attr = upspin.AttrNone
		de.Packing = upspin.PlainPack
		_, err = s.Put(de)
		if !errors.Match(c.err, err) {
			t.Errorf("%s: err = %v, want = %v", c.user, err, c.err)
		}
	}
}

func TestSnapshotWhichAccessIsNil(t *testing.T) {
	s, _ := newDirServerForTesting(t, canonicalUser)
	entry, err := s.WhichAccess(snapshotUser + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Fatalf("expected nil entry, got = %v", entry)
	}
}

func TestSnapshotUserCanCreateSnapshotRoot(t *testing.T) {
	s, _ := newDirServerForTesting(t, "user+snapshot@example.com")
	create(t, s, "user+snapshot@example.com/", isDir)
}

func create(t *testing.T, s *server, name upspin.PathName, isDir bool) {
	var err error
	if isDir {
		_, err = makeDirectory(s, name)
		if err != nil {
			t.Fatal(err)
		}
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
