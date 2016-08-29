// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// TODO: tests build upon earlier tests. This is brittle. Make it more hermetic
// by using testenv or something similar.

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"upspin.io/access"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/key/inprocess"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/inprocess"
)

const (
	userName   = "fred@flintstone.org"
	serverName = "dirserver@server.com"
)

var testDir string

func TestMakeRoot(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	de, err := s.MakeDirectory(userName + "/")
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
	err = checkDirEntry("TestMakeRoot", deLookup, de)
	if err != nil {
		t.Fatal(err)
	}

	// And we can't make a new root again.
	_, err = s.MakeDirectory(userName + "/")
	expectedErr := errors.E(errors.Exist)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %q, want = %q", err, expectedErr)
	}

	// Delete root works.
	_, err = s.Delete(userName + "/")
	if err != nil {
		t.Fatal(err)
	}

	// Create it again.
	_, err = s.MakeDirectory(userName + "/")
	if err != nil {
		t.Fatal(err)
	}
}

// Test that we can call MakeDirectory to make a root using only the user name
// without a slash. This was a bug.
func TestMakeRootNoSlash(t *testing.T) {
	const userName = "wilma@flintstone.org"
	s := newDirServerForTesting(t, userName)
	_, err := s.MakeDirectory(userName) // Note: No terminal slash on this name.
	if err != nil {
		t.Fatal(err)
	}
}

func TestPut(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	de := &upspin.DirEntry{
		Name:    userName + "/file1.txt",
		Attr:    upspin.AttrNone,
		Writer:  userName,
		Packing: upspin.PlainPack,
	}
	_, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de2, err := s.Lookup(de.Name)
	if err != nil {
		t.Fatal(err)
	}
	err = checkDirEntry("TestPut", de2, de)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMakeDirectory(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	de, err := s.MakeDirectory(userName + "/dir")
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
	err = checkDirEntry("TestMakeDirectory", de2, de)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLink(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	de := &upspin.DirEntry{
		Name:    userName + "/mylink",
		Attr:    upspin.AttrLink,
		Writer:  userName,
		Link:    "linkerdude@linkatron.lnk/target",
		Packing: upspin.PlainPack,
	}
	_, err := s.Put(de)
	if err != nil {
		t.Fatal(err)
	}
	de2, err := s.Lookup(de.Name)
	if err != nil {
		t.Fatal(err)
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
		Name:    userName + "/mylink/new_file.txt",
		Attr:    upspin.AttrNone,
		Writer:  userName,
		Packing: upspin.PlainPack,
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
	de2, err = s.MakeDirectory(userName + "/mylink/newdir")
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

	// Deletion of the link itself is tested in TestDelete (we need it
	// around for other tests, sadly).
}

func TestWhichAccess(t *testing.T) {
	const accessFile = "*: " + userName
	s := newDirServerForTesting(t, userName)
	de, err := putAccessFile(t, s, userName+"/Access", accessFile)
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
	de2, err := putAccessFile(t, s, userName+"/dir/Access", accessFile)
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
	accEntry, err = s.WhichAccess(userName + "/mylink")
	if err != nil {
		t.Fatal(err)
	}
	// The Access file for the link is the one for the parent (de).
	if err := checkDirEntry("TestWhichAccess.4", accEntry, de); err != nil {
		t.Fatal(err)
	}
}

func TestHasRight(t *testing.T) {
	const accessFile = "l,d: " + userName
	s := newDirServerForTesting(t, userName)
	_, err := putAccessFile(t, s, userName+"/Access", accessFile)
	if err != nil {
		t.Fatal(err)
	}
	p, err := path.Parse(userName + "/")

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

func TestGlob(t *testing.T) {
	const globberUser = "somedude@somewhere.com"

	sOwner := newDirServerForTesting(t, userName)

	// Put an Access file that has List permissions for newUser.
	_, err := putAccessFile(t, sOwner, userName+"/Access", "*:"+userName+"\nl:"+globberUser)
	if err != nil {
		t.Fatal(err)
	}

	// Get a server for globberUser.
	s := newDirServerForTesting(t, globberUser)

	//
	// First subtest: list someone else's root without Read rights.
	//

	ents, err := s.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}
	expected := []upspin.PathName{
		userName + "/Access",
		userName + "/dir",
		userName + "/file1.txt",
		userName + "/mylink",
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}

	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
		// Verify that Blocks and Packdata are nil, since we don't have
		// Read rights.
		if len(e.Blocks) != 0 {
			t.Errorf("len(e.Blocks) = %d, want = 0", len(e.Blocks))
		}
		if len(e.Packdata) != 0 {
			t.Errorf("len(e.Packdata) = %d, want = 0", len(e.Packdata))
		}
	}

	// Try globbing a specific file.
	ents, err = s.Glob(userName + "/file1.txt")
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	expected = []upspin.PathName{
		userName + "/file1.txt",
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	//
	// Second subtest: globber has Read permissions and Glob is more complex.
	//

	// Put an Access file where globber has Read permissions.
	_, err = putAccessFile(t, sOwner, userName+"/dir/Access", "*:"+userName+"\nl,r:"+globberUser)
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
		_, err = sOwner.MakeDirectory(userName + dir)
		if err != nil {
			t.Fatal(err)
		}
	}

	ents, err = s.Glob(userName + "/?ir/sub*")
	if err != nil {
		t.Fatal(err)
	}
	expected = []upspin.PathName{
		userName + "/dir/subdir",
		userName + "/dir/subway",
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
		// Since both dirs contain subdirs, verify that Blocks and
		// Packdata are not nil, because we have Read rights.
		if len(e.Blocks) == 0 {
			t.Errorf("len(e.Blocks) = %d, want > 0", len(e.Blocks))
		}
		if len(e.Packdata) == 0 {
			t.Errorf("len(e.Packdata) = %d, want > 0", len(e.Packdata))
		}
	}

	// Try globbing a specific directory not directly in the root.
	ents, err = s.Glob(userName + "/dir/foo")
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	expected = []upspin.PathName{
		userName + "/dir/foo",
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	//
	// Third subtest: More complex regex.
	//

	// Globber tries more complex glob.
	ents, err = s.Glob(userName + "/?ir/sub*")
	if err != nil {
		t.Fatal(err)
	}
	expected = []upspin.PathName{
		userName + "/dir/subdir",
		userName + "/dir/subway",
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	//
	// Fourth subtest: A deep regex by directory owner, now matching a link
	//                 in the middle.
	//

	// Owner Puts a link.
	de := &upspin.DirEntry{
		Name:    userName + "/dir/sublinkdir",
		Attr:    upspin.AttrLink,
		Writer:  userName,
		Link:    "linkerdude@linkatron.lnk/target",
		Packing: upspin.PlainPack,
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
	expected = []upspin.PathName{
		userName + "/dir/subdir/sub",
		userName + "/dir/sublinkdir", // Causes ErrFollowLink above.
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	// Glob the link itself.
	ents, err = sOwner.Glob(userName + "/dir/sublinkdir")
	expected = []upspin.PathName{
		userName + "/dir/sublinkdir",
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	//
	// Fifth subtest: globber can't list part of the path; only the first
	//                link is returned (the other is not visible).
	//

	// Put an Access file where globber does not have permissions in /dir.
	_, err = putAccessFile(t, sOwner, userName+"/dir/Access", "*:"+userName)
	if err != nil {
		t.Fatal(err)
	}

	// Globber tries to glob everything; gets partial view.
	ents, err = s.Glob(userName + "/*/*/*")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %q, want = %q (ErrFollowLink)", err, upspin.ErrFollowLink)
	}
	expected = []upspin.PathName{
		userName + "/mylink", // Causes ErrFollowLink above.
	}
	for _, e := range ents {
		t.Logf("got: %q", e.Name)
	}
	if got, want := len(ents), len(expected); got != want {
		t.Fatalf("len(ents) = %d, want = %d", got, want)
	}
	for i, e := range ents {
		if got, want := e.Name, expected[i]; got != want {
			t.Errorf("%d: e.Name = %q, want = %q", i, got, want)
		}
	}

	// Test syntax error.
	_, err = s.Glob(userName + "/[]")
	expectErr := errors.E(errors.Syntax)
	if !errors.Match(expectErr, err) {
		t.Fatalf("err = %q, want = %q", err, expectErr)
	}
}

func TestDelete(t *testing.T) {
	s := newDirServerForTesting(t, userName)

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
		"/file1.txt",
		"/mylink", // Deleting the link works.
	} {
		_, err = s.Delete(userName + dir)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestClose(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	s.Close()
	// TODO: check error code when we have one.
}

// Tests some error conditions too.

func TestPermissionDenied(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	// Access file permits only List rights.
	_, err := putAccessFile(t, s, userName+"/Access", "l:"+userName)
	if err != nil {
		t.Fatal(err)
	}
	de := &upspin.DirEntry{
		Name:    userName + "/some_new_file.txt",
		Attr:    upspin.AttrNone,
		Writer:  userName,
		Packing: upspin.PlainPack,
	}
	_, err = s.Put(de)
	if !errors.Match(access.ErrPermissionDenied, err) {
		t.Fatalf("err = %v, want = %v", err, access.ErrPermissionDenied)
	}
	_, err = s.MakeDirectory(userName + "/dir")
	if !errors.Match(access.ErrPermissionDenied, err) {
		t.Fatalf("err = %v, want = %v", err, access.ErrPermissionDenied)
	}

	// Now Access file permits Create right too.
	_, err = putAccessFile(t, s, userName+"/Access", "l,c:"+userName)
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

func TestOverwriteFileWithWrongSequence(t *testing.T) {
	s := newDirServerForTesting(t, userName)
	_, err := putAccessFile(t, s, userName+"/Access", "*:"+userName)
	if err != nil {
		t.Fatal(err)
	}
	de := &upspin.DirEntry{
		Name:     userName + "/some_new_file.txt",
		Attr:     upspin.AttrNone,
		Writer:   userName,
		Packing:  upspin.PlainPack,
		Sequence: 99,
	}
	_, err = s.Put(de)
	expectedErr := errors.E(errors.Invalid, errors.Str("sequence number"))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %v", err, expectedErr)
	}
}

func TestMain(m *testing.M) {
	// So we don't see a ton of "Metric channel is full" messages
	metric.RegisterSaver(&sinkSaver{})

	var err error
	testDir, err = ioutil.TempDir("", "DirServer")
	if err != nil {
		panic(err)
	}

	code := m.Run()

	os.RemoveAll(testDir)
	os.Exit(code)
}

func putAccessFile(t *testing.T, s *server, name upspin.PathName, contents string) (*upspin.DirEntry, error) {
	if !access.IsAccessFile(name) { // For internal consistency only.
		t.Fatalf("%s not an access file", name)
	}
	loc := writeToStore(t, s.serverContext, []byte(contents))
	de := &upspin.DirEntry{
		Name:    name,
		Attr:    upspin.AttrNone,
		Writer:  userName,
		Packing: upspin.PlainPack,
		Blocks: []upspin.DirBlock{
			{
				Location: loc,
				Offset:   0,
				Size:     int64(len(contents)),
			},
		},
	}
	_, err := s.Put(de)
	return de, err
}

// checkDirEntry compares the main fields in dir entries got and want and
// reports their differences.
func checkDirEntry(testName string, got, want *upspin.DirEntry) error {
	if got.Name != want.Name {
		return errors.Errorf("%s: got.Name = %q, want = %q", testName, got.Name, want.Name)
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
	return nil
}

var generatorInstance upspin.DirServer

func newDirServerForTesting(t *testing.T, userName upspin.UserName) *server {
	factotum, err := factotum.New(repo("key/testdata/upspin-test"))
	if err != nil {
		t.Fatal(err)
	}
	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	ctx := context.New().
		SetFactotum(factotum).
		SetUserName(serverName).
		SetStoreEndpoint(endpointInProcess).
		SetKeyEndpoint(endpointInProcess).
		SetDirEndpoint(endpointInProcess).
		SetPacking(upspin.EEPack)
	key := ctx.KeyServer()
	// Set the public key for the tree, since it must do Auth against the Store.
	user := &upspin.User{
		Name:      serverName,
		Dirs:      []upspin.Endpoint{ctx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: factotum.PublicKey(),
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Set the public key for the user, since EE Pack requires the dir owner
	// to have a wrapped key.
	userCtx := context.New().SetUserName(userName).SetDirEndpoint(ctx.DirEndpoint())
	user = &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{userCtx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: factotum.PublicKey(), // doesn't matter
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}
	if generatorInstance == nil {
		generatorInstance, err = New(ctx, "logDir="+testDir)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Get a new instance properly initialized for this user.
	svr, err := generatorInstance.Dial(userCtx, endpointInProcess)
	if err != nil {
		t.Fatal(err)
	}
	return svr.(*server)
}

func writeToStore(t *testing.T, ctx upspin.Context, data []byte) upspin.Location {
	store := ctx.StoreServer()
	ref, err := store.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	return upspin.Location{
		Endpoint:  store.Endpoint(),
		Reference: ref,
	}
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		panic("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}

type sinkSaver struct {
}

func (s *sinkSaver) Register(ch chan *metric.Metric) {
	go func() {
		for {
			<-ch
			// Drop it on the floor
		}
	}()
}

var _ metric.Saver = (*sinkSaver)(nil)
