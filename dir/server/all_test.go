// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// TODO: tests build upon earlier tests. This is brittle. Make it more hermetic
// by using testenv or something similar.

import (
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/dir/server/serverlog"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"
)

func init() {
	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, storeserver.New())
}

const (
	userName   = "fred@flintstone.org"
	serverName = "dirserver@server.com"
	otherUser  = "somedude@somewhere.com"
)

var (
	testDir  string
	mockTime *mockClock
)

func TestMakeRoot(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	de, err := makeDirectory(s, userName+"/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := de.Name, upspin.PathName(userName+"/"); got != want {
		t.Errorf("de.Name = %q, want = %q", got, want)
	}
	// Lookup confirms the de we got.
	deLookup, err := s.Lookup(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	deExpected := *de
	deExpected.Writer = serverName
	deExpected.Packing = upspin.EEPack
	deExpected.Sequence = upspin.SeqBase
	err = checkDirEntry("TestMakeRoot", deLookup, &deExpected)
	if err != nil {
		t.Fatal(err)
	}

	// And we can't make a new root again.
	_, err = makeDirectory(s, userName+"/")
	expectedErr := errors.E(errors.Exist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %q, want = %q", err, expectedErr)
	}

	// Delete root works.
	_, err = s.Delete(userName + "/")
	if err != nil {
		t.Fatal(err)
	}

	// Ensure log for user has been deleted.
	hasLog, err := serverlog.HasLog(userName, s.logDir)
	if err != nil {
		t.Fatal(err)
	}
	if hasLog {
		t.Fatalf("expected no log for user %q in %q", userName, s.logDir)
	}

	// Create it again.
	_, err = makeDirectory(s, userName+"/")
	if err != nil {
		t.Fatal(err)
	}
}

// Test that we can call MakeDirectory to make a root using only the user name
// without a slash. This was a bug.
func TestMakeRootNoSlash(t *testing.T) {
	const userName = "wilma@flintstone.org"
	s, _ := newDirServerForTesting(t, userName)
	_, err := makeDirectory(s, userName) // Note: No terminal slash on this name.
	if err != nil {
		t.Fatal(err)
	}
}

func TestPut(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	de := &upspin.DirEntry{
		Name:       userName + "/file1.txt",
		SignedName: userName + "/file1.txt",
		Attr:       upspin.AttrNone,
		Writer:     userName,
		Sequence:   upspin.SeqNotExist,
		Packing:    upspin.PlainPack,
	}
	entry, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("nil entry")
	}
	if !entry.IsIncomplete() {
		t.Fatal("non-incomplete entry")
	}
	de2, err := s.Lookup(de.Name)
	t.Log(de.Name, de2.Sequence, entry.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if de2.Sequence != entry.Sequence {
		t.Errorf("Lookup returned sequence %d; expected %d", de2.Sequence, entry.Sequence)
	}
	deExpected := *de
	deExpected.Sequence = upspin.SeqBase + 1
	err = checkDirEntry("TestPut", de2, &deExpected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMakeDirectory(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	de, err := makeDirectory(s, userName+"/dir")
	if err != nil {
		t.Fatal(err)
	}
	de2, err := s.Lookup(de.Name)
	if err != nil {
		t.Fatal(err)
	}
	if de2.Name != de.Name {
		t.Errorf("de2.Name = %q, want = %q", de2.Name, de.Name)
	}
	if de2.Attr != upspin.AttrDirectory {
		t.Errorf("de2.Att = %v, want = %v", de2.Attr, upspin.AttrDirectory)
	}
	deExpected := *de
	deExpected.Writer = serverName
	deExpected.Packing = upspin.EEPack
	deExpected.Sequence = de2.Sequence
	err = checkDirEntry("TestMakeDirectory", de2, &deExpected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLink(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	de := &upspin.DirEntry{
		Name:       userName + "/mylink",
		SignedName: userName + "/mylink",
		Attr:       upspin.AttrLink,
		Writer:     userName,
		Link:       "linkerdude@linkatron.lnk/target",
		Packing:    upspin.PlainPack,
	}
	_, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de2, err := s.Lookup(de.Name)
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink", de2, de)
	if err != nil {
		t.Fatal(err)
	}
	// Lookup something past the link entry.
	de2, err = s.Lookup(userName + "/mylink/landing_place.jpg")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.Lookup", de2, de)
	if err != nil {
		t.Fatal(err)
	}
	// Put file into linked destination
	deAfterLink := &upspin.DirEntry{
		Name:       userName + "/mylink/new_file.txt",
		SignedName: userName + "/mylink/new_file.txt",
		Attr:       upspin.AttrNone,
		Writer:     userName,
		Packing:    upspin.PlainPack,
	}
	de2, err = s.Put(deAfterLink)
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.Put", de2, de)
	if err != nil {
		t.Fatal(err)
	}

	// Try to MakeDirectory under the link.
	de2, err = makeDirectory(s, userName+"/mylink/newdir")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.Mkdir", de2, de)
	if err != nil {
		t.Fatal(err)
	}

	// Call WhichAccess under the link.
	de2, err = s.WhichAccess(userName + "/mylink/will_return_follow_link")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.WhichAccess", de2, de)
	if err != nil {
		t.Fatal(err)
	}

	// Delete something at the other side of the link.
	de2, err = s.Delete(userName + "/mylink/will_return_follow_link")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v, want = ErrFollowLink (%v)", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.Lookup", de2, de)
	if err != nil {
		t.Fatal(err)
	}

	// Get a server for otherUser, who has no right to see the link.
	sOther, userCtx := newDirServerForTesting(t, otherUser)
	_, err = sOther.Lookup(userName + "/mylink")
	if !errors.Is(errors.Private, err) {
		t.Errorf("err = %v, want = %v", err, errPrivate)
	}

	// Now give otherUser some right.
	_, err = putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "*:"+userName+"\nc:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}
	de2, err = sOther.Lookup(userName + "/mylink")
	if err != upspin.ErrFollowLink {
		t.Errorf("err = %v, want = %v", err, upspin.ErrFollowLink)
	}
	err = checkDirEntry("TestLink.LookupOther", de2, de)
	if err != nil {
		t.Fatal(err)
	}

	// Deletion of the link itself is tested in TestDelete (we need it
	// around for other tests, sadly).
}

func TestWhichAccess(t *testing.T) {
	const accessFile = "*: " + userName
	s, userCtx := newDirServerForTesting(t, userName)
	de, err := putAccessOrGroupFile(t, s, userCtx, userName+"/Access", accessFile)
	if err != nil {
		t.Fatal(err)
	}
	// Check the root.
	accEntry, err := s.WhichAccess(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDirEntry("TestWhichAccess.1", accEntry, de); err != nil {
		t.Fatal(err)
	}
	// Check dir1, still the same Access file at the root.
	accEntry, err = s.WhichAccess(userName + "/dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDirEntry("TestWhichAccess.2", accEntry, de); err != nil {
		t.Fatal(err)
	}
	// Add Access to dir1. New answer.
	de2, err := putAccessOrGroupFile(t, s, userCtx, userName+"/dir/Access", accessFile)
	if err != nil {
		t.Fatal(err)
	}
	accEntry, err = s.WhichAccess(userName + "/dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDirEntry("TestWhichAccess.3", accEntry, de2); err != nil {
		t.Fatal(err)
	}

	// Check that links work.
	link := upspin.PathName(userName + "/mylink")
	accEntry, err = s.WhichAccess(link)
	if err != upspin.ErrFollowLink {
		t.Fatal("want ErrFollowLink, got", err)
	}
	// WhichAccess should return the link itself for a link.
	if accEntry.Name != link {
		t.Fatalf("WhichAccess(link) returned %q, want %q", accEntry.Name, link)
	}

	// Test that Access files don't cause weird loops.
	accEntry, err = s.WhichAccess(userName + "/dir/Access")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDirEntry("TestWhichAccess.4", accEntry, de2); err != nil {
		t.Fatal(err)
	}
}

func TestHasRight(t *testing.T) {
	const accessFile = "l,d: " + userName
	s, userCtx := newDirServerForTesting(t, userName)
	_, err := putAccessOrGroupFile(t, s, userCtx, userName+"/Access", accessFile)
	if err != nil {
		t.Fatal(err)
	}
	p, err := path.Parse(userName + "/")
	if err != nil {
		t.Fatal(err)
	}

	checkAccess := func(right access.Right, want bool) error {
		hasAccess, _, err := s.hasRight(right, p)
		if err != nil {
			return err
		}
		if want != hasAccess {
			return errors.Errorf("%s: right %v: hasAccess = %v, want = %v", p.Path(), right, hasAccess, want)
		}
		return nil
	}

	for _, test := range []struct {
		right    access.Right
		expected bool
	}{
		{access.List, true}, // owner always has List access.
		{access.Read, true}, // owner always has Read access.
		{access.Create, false},
		{access.Write, false},
		{access.Delete, true},
	} {
		// Check whether userName has each of the rights.
		err = checkAccess(test.right, test.expected)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Check regression: This was a bug in the Tree.
func TestGlobDoesNotRemoveRoot(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	// Forces a flush on the user tree.
	ents1, err := s.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}

	// Forget this user's tree (force a new Tree object to be re-created).
	val := s.userTrees.Remove(upspin.UserName(userName))
	if val == nil {
		t.Fatal("Expected existing value, got nil")
	}

	ents2, err := s.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(ents2), len(ents1); got != want {
		t.Fatalf("Tree got corrupted. len(ents) = %d, want = %d", got, want)
	}
}

// Check regression: This was a bug in serverutil.Glob.
func TestGlobMultiSlashPattern(t *testing.T) {
	const (
		otherUser       = "joe@somewhere.com"
		dirName         = otherUser + "/"
		pathName        = dirName + "x"
		oneSlashPattern = dirName + "*"
		twoSlashPattern = dirName + "/*"
	)
	s, _ := newDirServerForTesting(t, otherUser)
	entry := &upspin.DirEntry{
		Name:       dirName,
		SignedName: dirName,
		Packing:    upspin.PlainPack,
		Attr:       upspin.AttrDirectory,
		Writer:     serverName,
		Sequence:   upspin.SeqIgnore,
	}
	_, err := s.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry = &upspin.DirEntry{
		Name:       pathName,
		SignedName: pathName,
		Packing:    upspin.PlainPack,
		Writer:     serverName,
		Sequence:   upspin.SeqIgnore,
	}
	_, err = s.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := s.Glob(oneSlashPattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatal("no results from Glob with one slash")
	}
	ents, err = s.Glob(twoSlashPattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatal("no results from Glob with two slashes")
	}
}

func TestGlob(t *testing.T) {
	sOwner, ownerCtx := newDirServerForTesting(t, userName)

	// Put an Access file that has List permissions for newUser.
	_, err := putAccessOrGroupFile(t, sOwner, ownerCtx, userName+"/Access", "*:"+userName+"\nl:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}

	// Get a server for otherUser.
	s, _ := newDirServerForTesting(t, otherUser)

	//
	// First subtest: list someone else's root without Read rights.
	//

	ents, err := s.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}
	const incomplete = true
	type expected struct {
		name       upspin.PathName
		incomplete bool
	}
	exp := []expected{
		{userName + "/Access", !incomplete},   // available with any right.
		{userName + "/dir", incomplete},       // marked incomplete (no read rights).
		{userName + "/file1.txt", incomplete}, // marked incomplete (no read rights).
		{userName + "/mylink", incomplete},    // links never have blocks nor metadata.
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}

	verify := func(ents []*upspin.DirEntry, exp []expected) {
		for _, e := range ents {
			t.Logf("got: %q", e.Name)
		}
		if got, want := len(ents), len(exp); got != want {
			t.Fatalf("len(ents) = %d, want = %d", got, want)
		}
		for i, e := range ents {
			if got, want := e.Name, exp[i].name; got != want {
				t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
			}
			// Verify whether entry is marked incomplete.
			if got, want := e.IsIncomplete(), exp[i].incomplete; got != want {
				t.Errorf("%s: incomplete = %v, want = %v", e.Name, got, want)
			}
		}
	}
	verify(ents, exp)

	// Try globbing a specific file.
	ents, err = s.Glob(userName + "/file1.txt")
	if err != nil {
		t.Fatal(err)
	}
	exp = []expected{
		{userName + "/file1.txt", incomplete},
	}
	verify(ents, exp)

	//
	// Second subtest: globber has Read permissions and Glob is more complex.
	//

	// Put an Access file where globber has Read permissions.
	_, err = putAccessOrGroupFile(t, sOwner, ownerCtx, userName+"/dir/Access", "*:"+userName+"\nl,r:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}

	// Add stuff to dir, to check more complex Globs.
	for _, dir := range []upspin.PathName{
		"/dir/subdir",
		"/dir/subway",
		"/dir/foo",
		"/dir/bar",
		"/dir/subdir/sub",
		"/dir/subdir/blub",
		"/dir/subway/meh",
	} {
		_, err = makeDirectory(sOwner, userName+dir)
		if err != nil {
			t.Fatal(err)
		}
	}

	ents, err = s.Glob(userName + "/?ir/sub*")
	if err != nil {
		t.Fatal(err)
	}
	exp = []expected{
		{userName + "/dir/subdir", !incomplete},
		{userName + "/dir/subway", !incomplete},
	}
	verify(ents, exp)

	// Try globbing a specific directory not directly in the root.
	ents, err = s.Glob(userName + "/dir/foo")
	if err != nil {
		t.Fatal(err)
	}
	exp = []expected{
		{userName + "/dir/foo", !incomplete},
	}
	verify(ents, exp)

	//
	// Third subtest: More complex regex.
	//

	// Globber tries more complex glob.
	ents, err = s.Glob(userName + "/?ir/sub*")
	if err != nil {
		t.Fatal(err)
	}
	exp = []expected{
		{userName + "/dir/subdir", !incomplete},
		{userName + "/dir/subway", !incomplete},
	}
	verify(ents, exp)

	//
	// Fourth subtest: A deep regex by directory owner, now matching a link
	//                 in the middle.
	//

	// Owner Puts a link.
	de := &upspin.DirEntry{
		Name:       userName + "/dir/sublinkdir",
		SignedName: userName + "/dir/sublinkdir",
		Attr:       upspin.AttrLink,
		Writer:     userName,
		Link:       "linkerdude@linkatron.lnk/target",
		Packing:    upspin.PlainPack,
	}
	_, err = sOwner.Put(de)
	if err != nil {
		t.Fatal(err)
	}

	// Glob spans the link.
	ents, err = sOwner.Glob(userName + "/?ir/*dir/s*")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %q, want = %q (ErrFollowLink)", err, upspin.ErrFollowLink)
	}
	exp = []expected{
		{userName + "/dir/subdir/sub", !incomplete},
		{userName + "/dir/sublinkdir", !incomplete}, // Causes ErrFollowLink above.
	}
	verify(ents, exp)

	// Glob the link itself.
	ents, err = sOwner.Glob(userName + "/dir/sublinkdir")
	if err != nil {
		t.Fatalf("Glob returned error %v, want nil", err)
	}
	exp = []expected{
		{userName + "/dir/sublinkdir", !incomplete},
	}
	verify(ents, exp)

	//
	// Fifth subtest: globber can't list part of the path; only the first
	//                link is returned (the other is not visible).
	//

	// Put an Access file where globber does not have permissions in /dir.
	_, err = putAccessOrGroupFile(t, sOwner, ownerCtx, userName+"/dir/Access", "*:"+userName)
	if err != nil {
		t.Fatal(err)
	}

	// Globber tries to glob everything; gets partial view.
	ents, err = s.Glob(userName + "/*/*/*")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %q, want = %q (ErrFollowLink)", err, upspin.ErrFollowLink)
	}
	exp = []expected{
		{userName + "/mylink", incomplete}, // Causes ErrFollowLink above.
	}
	verify(ents, exp)

	// Test syntax error.
	_, err = s.Glob(userName + "/[]")
	expectErr := errors.E(errors.Invalid)
	if !errors.Match(expectErr, err) {
		t.Fatalf("err = %q, want = %q", err, expectErr)
	}
}

func TestDeletePermission(t *testing.T) {
	s, userCtx := newDirServerForTesting(t, userName)
	sOther, _ := newDirServerForTesting(t, otherUser)

	// Only owner has any right.
	_, err := putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "*:"+userName)
	if err != nil {
		t.Fatal(err)
	}

	fileName := upspin.PathName(userName + "/file1.txt")
	_, err = sOther.Delete(fileName)
	expectedErr := errPrivate
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Owner allows other to see file.
	_, err = putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "*:"+userName+"\nl:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sOther.Delete(fileName)
	expectedErr = errors.E(errors.Permission, fileName)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Owner allows other to delete the file.
	_, err = putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "*:"+userName+"\nd:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}

	_, err = sOther.Delete(fileName)
	if err != nil {
		t.Fatal(err)
	}

	// Owner can delete too (tested elsewhere).
}

func TestDelete(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)

	// Directory not empty (there are entries there).
	_, err := s.Delete(userName + "/dir")
	expectedErr := errors.E(errors.NotEmpty)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}

	// Owner can remove contents. Order matters, we remove subdirs first.
	for _, dir := range []upspin.PathName{
		"/dir/Access",
		"/dir/subdir/sub",
		"/dir/subdir/blub",
		"/dir/subdir",
		"/dir/sublinkdir",
		"/dir/subway/meh",
		"/dir/subway",
		"/dir/foo",
		"/dir/bar",
		"/dir", // Deleting dir now works.
		"/Access",
		"/mylink", // Deleting the link works.
	} {
		_, err = s.Delete(userName + dir)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestForgetsRemoteGroupFiles(t *testing.T) {
	// Give otherUser read access through someone else's family Group.
	const (
		familyGroupName     = "foo@example.com/Group/family"
		familyGroupContents = otherUser

		accessFile     = userName + "/Access"
		accessContents = "r: " + familyGroupName
	)

	s, userCtx := newDirServerForTesting(t, userName)
	_, err := putAccessOrGroupFile(t, s, userCtx, accessFile, accessContents)
	if err != nil {
		t.Fatal(err)
	}

	sReader, _ := newDirServerForTesting(t, otherUser)
	_, err = sReader.Lookup(accessFile)
	if !errors.Is(errors.Private, err) {
		t.Errorf("err = %s\nwant = %q", err, errPrivate)
	}

	// Simulate we loaded the family Group through some remote server.
	s.remoteGroups.Add(upspin.PathName(familyGroupName), lastLoad(mockTime.now()))
	err = access.AddGroup(familyGroupName, []byte(familyGroupContents))
	if err != nil {
		t.Fatal(err)
	}

	// Check that reader has read access.
	_, err = sReader.Lookup(accessFile)
	if err != nil {
		t.Errorf("Expected no error, got = %s", err)
	}

	// Now time moves forward and the Group file granting reader read right
	// expires.
	mockTime.addSecond(10) // 10 seconds later (expiration time is just a milli).
	time.Sleep(2 * remoteGroupDuration)

	// Lookup now fails because the Group file granting permission is not
	// found (no server for foo@example.com).
	_, err = sReader.Lookup(accessFile)
	if !errors.Is(errors.Private, err) {
		t.Errorf("err = %s, want = %q", err, errPrivate)
	}
}

func TestClose(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)
	s.Close()
	// TODO: check error code when we have one.
}

// Tests some error conditions too.

// Ensures no one can figure out which users exist by looking them up and
// differentiating a non-existing root from a permission-denied root.
func TestCantProbeForExistence(t *testing.T) {
	s, _ := newDirServerForTesting(t, userName)

	_, err := s.Lookup("barney@rubble.org/")
	if !errors.Is(errors.NotExist, err) {
		t.Fatalf("err = %v, want = %v", err, errNotExist)
	}
}

func TestPermissionDenied(t *testing.T) {
	s, userCtx := newDirServerForTesting(t, userName)
	// Access file permits only List rights.
	_, err := putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "l:"+userName)
	if err != nil {
		t.Fatal(err)
	}
	de := &upspin.DirEntry{
		Name:       userName + "/some_new_file.txt",
		SignedName: userName + "/some_new_file.txt",
		Attr:       upspin.AttrNone,
		Writer:     userName,
		Packing:    upspin.PlainPack,
	}
	_, err = s.Put(de)
	if !errors.Match(access.ErrPermissionDenied, err) {
		t.Fatalf("err = %v, want = %v", err, access.ErrPermissionDenied)
	}
	_, err = makeDirectory(s, userName+"/dir")
	if !errors.Match(access.ErrPermissionDenied, err) {
		t.Fatalf("err = %v, want = %v", err, access.ErrPermissionDenied)
	}

	// Now Access file permits Create right too.
	_, err = putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "l,c:"+userName)
	if err != nil {
		t.Fatal(err)
	}

	// Now a new file can be Put.
	_, err = s.Put(de)
	if err != nil {
		t.Fatal(err)
	}

	// But can't be overwritten (lacks Write permission).
	_, err = s.Put(de)
	if !errors.Match(access.ErrPermissionDenied, err) {
		t.Fatalf("err = %v, want = %v", err, access.ErrPermissionDenied)
	}
}

func TestAccessAndGroupFilesNotIncomplete(t *testing.T) {
	const userAccess = userName + "/Access"
	s, userCtx := newDirServerForTesting(t, userName)
	// Access file permits List rights for otherUser.
	_, err := putAccessOrGroupFile(t, s, userCtx, userAccess, "l:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}
	sOther, _ := newDirServerForTesting(t, otherUser)

	entry, err := sOther.Lookup(userAccess)
	if err != nil {
		t.Fatal(err)
	}
	if entry.IsIncomplete() {
		t.Fatal("Got incomplete entry, expected blocks")
	}
}

func TestAccessAndGroupFilesNotIncompleteFromWatch(t *testing.T) {
	const userAccess = userName + "/Access"
	s, userCtx := newDirServerForTesting(t, userName)
	// Access file permits List rights for otherUser.
	_, err := putAccessOrGroupFile(t, s, userCtx, userAccess, "l:"+otherUser)
	if err != nil {
		t.Fatal(err)
	}
	sOther, _ := newDirServerForTesting(t, otherUser)

	done := make(chan struct{})
	defer close(done)
	events, err := sOther.Watch(userName+"/", -1, done)
	if err != nil {
		t.Fatal(err)
	}

	for _, exp := range []struct {
		name       upspin.PathName
		incomplete bool
	}{
		{userName + "/", true},
		{userName + "/Access", false},
	} {
		var event upspin.Event
		select {
		case event = <-events:
			// Ok
		case <-time.After(time.Minute):
			t.Errorf("Timed out waiting for event")
		}
		entry := event.Entry

		if entry.SignedName != exp.name {
			t.Fatalf("got %s, want = %s", entry.SignedName, exp.name)
		}
		if entry.IsIncomplete() && !exp.incomplete {
			t.Fatalf("Got incomplete entry (%s), expected blocks", event.Entry.Name)
		} else if !entry.IsIncomplete() && exp.incomplete {
			t.Fatalf("Got complete entry (%s), expected incomplete", event.Entry.Name)
		}
	}
}

func TestOverwriteFileWithWrongSequence(t *testing.T) {
	s, userCtx := newDirServerForTesting(t, userName)
	_, err := putAccessOrGroupFile(t, s, userCtx, userName+"/Access", "*:"+userName)
	if err != nil {
		t.Fatal(err)
	}
	de := &upspin.DirEntry{
		Name:       userName + "/some_new_file.txt",
		SignedName: userName + "/some_new_file.txt",
		Attr:       upspin.AttrNone,
		Writer:     userName,
		Packing:    upspin.PlainPack,
		Sequence:   99,
	}
	_, err = s.Put(de)
	expectedErr := errors.E(errors.Invalid, "sequence number")
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestPutBadAccess(t *testing.T) {
	s, userCtx := newDirServerForTesting(t, userName)

	const accessFileContents = "Merge: batman@gotham.city"

	accessName := upspin.PathName(userName + "/Access")
	_, err := putAccessOrGroupFile(t, s, userCtx, accessName, accessFileContents)
	expectedErr := errors.E(errors.Invalid, errors.E(accessName))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestPutBadGroup(t *testing.T) {
	s, userCtx := newDirServerForTesting(t, userName)
	_, err := makeDirectory(s, userName+"/Group")
	if err != nil {
		t.Fatal(err)
	}

	const groupFileContents = "yo@bar" // bad username.

	groupName := upspin.PathName(userName + "/Group/badgroup")
	_, err = putAccessOrGroupFile(t, s, userCtx, groupName, groupFileContents)
	expectedErr := errors.E(errors.Invalid, errors.E(groupName))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestMain(m *testing.M) {
	var err error
	testDir, err = ioutil.TempDir("", "DirServer")
	if err != nil {
		panic(err)
	}

	code := m.Run()

	os.RemoveAll(testDir)
	os.Exit(code)
}

func makeDirectory(s *server, name upspin.PathName) (*upspin.DirEntry, error) {
	// Name must be clean, which includes having a final / for a user root.
	parsed, err := path.Parse(name)
	if err != nil {
		panic(err)
	}
	entry := &upspin.DirEntry{
		Name:       parsed.Path(),
		SignedName: parsed.Path(),
		Attr:       upspin.AttrDirectory,
		Sequence:   upspin.SeqIgnore,
		// Mimic what the client does -- it does not include any other field.
	}
	e, err := s.Put(entry)
	if err != nil {
		return e, err
	}
	entry.Sequence = e.Sequence
	return entry, nil
}

func putAccessOrGroupFile(t testing.TB, s *server, userCtx upspin.Config, name upspin.PathName, contents string) (*upspin.DirEntry, error) {
	if !access.IsAccessControlFile(name) {
		t.Fatalf("%s not an access file", name)
	}
	packer := pack.Lookup(upspin.EEIntegrityPack)
	de := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Link:       "",
		Time:       upspin.Now(),
		Sequence:   upspin.SeqIgnore,
		Attr:       upspin.AttrNone,
		Writer:     userName,
		Packing:    upspin.EEIntegrityPack,
	}
	bp, err := packer.Pack(userCtx, de)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := bp.Pack([]byte(contents))
	if err != nil {
		t.Fatal(err)
	}
	loc := writeToStore(t, userCtx, cipher)
	bp.SetLocation(
		upspin.Location{
			Endpoint:  loc.Endpoint,
			Reference: loc.Reference,
		},
	)
	err = bp.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Put(de)
	return de, err
}

// checkDirEntry compares the main fields in dir entries got and want and
// reports their differences.
func checkDirEntry(testName string, got, want *upspin.DirEntry) error {
	if got == nil {
		return errors.Errorf("%s: got nil entry", testName)
	}
	if got.Name != want.Name {
		return errors.Errorf("%s: got.Name = %q, want = %q", testName, got.Name, want.Name)
	}
	if got.SignedName != want.SignedName {
		return errors.Errorf("%s: got.SignedName = %q, want = %q", testName, got.SignedName, want.SignedName)
	}
	if got.Writer != want.Writer {
		return errors.Errorf("%s: got.Writer = %q, want = %q", testName, got.Writer, want.Writer)
	}
	if got.Attr != want.Attr {
		return errors.Errorf("%s: got.Attr = %v, want = %v", testName, got.Attr, want.Attr)
	}
	if got.Packing != want.Packing {
		return errors.Errorf("%s: got.Packing = %q, want = %q", testName, got.Packing, want.Packing)
	}
	if got.Sequence != want.Sequence {
		return errors.Errorf("%s: got.Sequence = %d, want = %d", testName, got.Sequence, want.Sequence)
	}
	return nil
}

type mockClock struct {
	mu   sync.Mutex
	time upspin.Time
}

func newMockClock(now upspin.Time) *mockClock {
	return &mockClock{time: now}
}

func (m *mockClock) now() upspin.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.time
}

func (m *mockClock) addSecond(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.time += upspin.Time(n)
}

func (m *mockClock) set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.time = upspin.TimeFromGo(t)
}

var generatorInstance upspin.DirServer

// newDirServerForTesting returns a new server and a user config.
func newDirServerForTesting(t testing.TB, userName upspin.UserName) (*server, upspin.Config) {
	return newDirServerForTestingWithTestDir(t, userName, testDir)
}

// newDirServerForTestingWithTestDir returns a new server using testDir for its
// logs and a user config for a user.
func newDirServerForTestingWithTestDir(t testing.TB, userName upspin.UserName, testDir string) (*server, upspin.Config) {
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "test"))
	if err != nil {
		t.Fatal(err)
	}
	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	ctx := config.New()
	ctx = config.SetUserName(ctx, serverName)
	ctx = config.SetPacking(ctx, upspin.EEPack)
	ctx = config.SetFactotum(ctx, f)
	ctx = config.SetKeyEndpoint(ctx, endpointInProcess)
	ctx = config.SetStoreEndpoint(ctx, endpointInProcess)
	ctx = config.SetDirEndpoint(ctx, endpointInProcess)

	key, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		t.Fatal(err)
	}

	// Set the public key for the tree, since it must do Auth against the Store.
	user := &upspin.User{
		Name:      serverName,
		Dirs:      []upspin.Endpoint{ctx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: f.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Set the public key for the user, since EE Pack requires the dir owner
	// to have a wrapped key.
	userCtx := config.New()
	userCtx = config.SetUserName(userCtx, userName)
	userCtx = config.SetPacking(userCtx, upspin.EEPack)
	userCtx = config.SetDirEndpoint(userCtx, ctx.DirEndpoint())
	userCtx = config.SetStoreEndpoint(userCtx, endpointInProcess)
	f, err = factotum.NewFromDir(testutil.Repo("key", "testdata", "bob"))
	if err != nil {
		t.Fatal(err)
	}
	userCtx = config.SetFactotum(userCtx, f)
	user = &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{userCtx.DirEndpoint()},
		Stores:    []upspin.Endpoint{userCtx.StoreEndpoint()},
		PublicKey: f.PublicKey(), // doesn't matter
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}
	if generatorInstance == nil {
		remoteGroupDuration = 50 * time.Millisecond
		mockTime = newMockClock(upspin.Now())
		generatorInstance, err = New(ctx, "logDir="+testDir)
		if err != nil {
			t.Fatal(err)
		}
		generatorInstance.(*server).now = func() upspin.Time { return mockTime.now() }
	}
	// Get a new instance properly initialized for this user.
	svr, err := generatorInstance.Dial(userCtx, endpointInProcess)
	if err != nil {
		t.Fatal(err)
	}
	return svr.(*server), userCtx
}

func writeToStore(t testing.TB, ctx upspin.Config, data []byte) upspin.Location {
	store, err := bind.StoreServer(ctx, ctx.StoreEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	refdata, err := store.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	return upspin.Location{
		Endpoint:  store.Endpoint(),
		Reference: refdata.Reference,
	}
}
