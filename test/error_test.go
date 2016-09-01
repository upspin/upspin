// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"

	"upspin.io/errors"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const writerName = readerName

// testGetErrors checks that the client receives 'not exist' or
// 'permission' errors where appropriate. In particular, it
// makes sure that the DirServer implementations do not allow
// unauthorized users to probe the name space. The rule of thumb
// is that users should receive 'not exist' for any path
// to which they have no rights.
func testGetErrors(t *testing.T, r *testenv.Runner) {
	const (
		base    = ownerName + "/get-errors"
		dir     = base + "/dir"
		file    = dir + "/file"
		access  = dir + "/Access"
		content = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	r.Put(file, content)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// We expect a "not exist" error for a reader that has no rights
	// to the file, as they cannot even know that it exists.
	r.As(readerName)
	r.Get(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put in an access file for the owner only
	// and try the same reader check again.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.As(readerName)
	r.Get(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Give the reader the a specific (non-read) right and
	// it should see a permission error.
	for _, right := range []string{"list", "write", "create", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+readerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(readerName)
		r.Get(file)
		if !r.Match(errors.E(errors.Permission)) {
			t.Fatalf("%s: %s", right, r.Diag())
		}
	}

	// Give the reader the "read" right and it works.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\nread:"+readerName)
	r.Put(file, content) // Put the file again to wrap the keys.
	r.As(readerName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// testGetLinkErrors is like testGetErrors but checks the
// behavior of Get when links are present.
func testGetLinkErrors(t *testing.T, r *testenv.Runner) {
	const (
		base      = ownerName + "/get-link-errors"
		srcDir    = base + "/src"
		dstDir    = base + "/dst"
		link      = srcDir + "/link"
		file      = dstDir + "/file"
		srcAccess = srcDir + "/Access"
		dstAccess = dstDir + "/Access"
		content   = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(srcDir)
	r.MakeDirectory(dstDir)
	r.Put(file, content)
	r.Get(file)
	r.PutLink(file, link)
	r.Get(link)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != content {
		t.Fatalf("link content = %q, want %q", r.Data, content)
	}

	// Make sure we get ErrFollowLink when looking up the link.
	r.DirLookup(link)
	if !r.Match(upspin.ErrFollowLink) {
		t.Fatal(r.Diag())
	}

	// As a user with no rights over the link,
	// we should get a 'not exist' error.
	r.As(readerName)
	r.Get(link)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(link)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Give the user rights over the link's target,
	// but still no rights over the link. The user should
	// be able to get the file, but still can't see the link.
	r.As(ownerName)
	r.Put(dstAccess, "*:"+ownerName+"\n*:"+readerName)
	r.Put(file, content) // Put the file again to wrap the keys.
	r.As(readerName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.Get(link)
	if !r.Match(errors.E(errors.NotExist, upspin.PathName(link))) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(link)
	if !r.Match(errors.E(errors.NotExist, upspin.PathName(link))) {
		t.Fatal(r.Diag())
	}

	// Give the user one right over the link and Get should work
	// as the client will now be able to follow the link.
	r.As(ownerName)
	r.Put(srcAccess, "create:"+readerName)
	r.As(readerName)
	r.Get(link)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.DirLookup(link)
	if !r.Match(upspin.ErrFollowLink) {
		t.Fatal(r.Diag())
	}

	// Remove the user's rights to the link target,
	// now a get of the link should fail with 'not exist' for the target.
	r.As(ownerName)
	r.Delete(dstAccess)
	r.As(readerName)
	r.Get(link)
	if !r.Match(errors.E(errors.NotExist, errors.E(upspin.PathName(file)))) {
		t.Fatal(r.Diag())
	}

	// Add a non-read permission for the target,
	// now a get of the link should fail with 'permission' for the target.
	r.As(ownerName)
	r.Put(dstAccess, "list:"+readerName)
	r.As(readerName)
	r.Get(link)
	if !r.Match(errors.E(errors.Permission, errors.E(upspin.PathName(file)))) {
		t.Fatal(r.Diag())
	}
}

func testPutErrors(t *testing.T, r *testenv.Runner) {
	const (
		base    = ownerName + "/put-errors"
		dir     = base + "/dir"
		file    = dir + "/file"
		access  = dir + "/Access"
		content = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	r.Put(file, content)
	r.Get(file)
	r.Delete(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// We expect a "not exist" error for a writer that has no rights
	// to the directory, as they cannot even know that it exists.
	r.As(writerName)
	r.Put(file, content)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put in an access file for the owner only
	// and try the same writer check again.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.As(writerName)
	r.Put(file, content)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Give the writer the a specific (non-write) right and
	// it should see a permission error.
	for _, right := range []string{"list", "read", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+writerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(writerName)
		r.Put(file, content)
		if !r.Match(errors.E(errors.Permission)) {
			t.Fatalf("%s: %s", right, r.Diag())
		}
	}

	// Give the reader the "create" right and it works.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\ncreate:"+writerName)
	r.As(writerName)
	r.Put(file, content)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// Try to overwrite, and it should fail.
	r.Put(file, content)
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}

	// Give the reader the "write" right and overwrite works.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\nwrite:"+writerName)
	r.As(writerName)
	r.Put(file, content)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Get should fail as the writer.
	r.Get(file)
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}

	// But should succeed as the owner.
	r.As(ownerName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Give the writer read access, and Get should succeed.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\nread:"+writerName)
	r.As(writerName)
	r.Get(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// TODO
func testPutLinkErrors(t *testing.T, r *testenv.Runner) {}

func testMakeDirectoryErrors(t *testing.T, r *testenv.Runner) {
	const (
		base   = ownerName + "/makedirectory-errors"
		dir    = base + "/dir"
		subdir = dir + "/subdir"
		access = dir + "/Access"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	r.MakeDirectory(subdir)
	r.DirLookup(subdir)
	r.Delete(subdir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// We expect a "not exist" error for a writer that has no rights
	// to the directory, as they cannot even know that it exists.
	r.As(writerName)
	r.MakeDirectory(subdir)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put in an access file for the owner only
	// and try the same writer check again.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.As(writerName)
	r.MakeDirectory(subdir)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Give the writer the a specific (non-create) right and
	// it should see a permission error.
	for _, right := range []string{"list", "read", "write", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+writerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(writerName)
		r.MakeDirectory(subdir)
		if !r.Match(errors.E(errors.Permission)) {
			t.Fatalf("%s: %s", right, r.Diag())
		}
	}

	// Give the reader the "create" right and it works.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\ncreate:"+writerName)
	r.As(writerName)
	r.MakeDirectory(subdir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// Try to make it again, and it should fail.
	r.MakeDirectory(subdir)
	if !r.Match(errors.E(errors.Exist)) {
		t.Fatal(r.Diag())
	}

	// Lookup should fail as the writer.
	r.DirLookup(subdir)
	if !r.Match(errors.E(errors.Permission)) {
		t.Fatal(r.Diag())
	}

	// But should succeed as the owner.
	r.As(ownerName)
	r.DirLookup(subdir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Give the writer read access, and lookup should succeed.
	r.As(ownerName)
	r.Put(access, "*:"+ownerName+"\nread:"+writerName)
	r.As(writerName)
	r.DirLookup(subdir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// TODO
func testMakeDirectoryLinkErrors(t *testing.T, r *testenv.Runner) {}

func testWhichAccessErrors(t *testing.T, r *testenv.Runner) {
	const (
		base       = ownerName + "/whichaccess-errors"
		dir        = base + "/dir"
		file       = dir + "/file"
		baseAccess = base + "/Access"
		dirAccess  = dir + "/Access"
		content    = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// The owner should get nil and no error
	// with no access file present.
	r.DirWhichAccess(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Entry != nil {
		t.Fatalf("got entry %q, expected nil", r.Entry.Name)
	}

	// A reader with no rights should get 'not exist' for the same.
	r.As(readerName)
	r.DirWhichAccess(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put in an access file in the root, the owner should see it.
	r.As(ownerName)
	r.Put(baseAccess, "*:"+ownerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(baseAccess) {
		t.Fatal(r.Diag())
	}

	// While the reader should still get not exist.
	r.As(readerName)
	r.DirWhichAccess(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put an access file in dir, we should get that one.
	r.As(ownerName)
	r.Put(dirAccess, "*:"+ownerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(dirAccess) {
		t.Fatal(r.Diag())
	}

	// The reader still gets bupkis.
	r.As(readerName)
	r.DirWhichAccess(file)
	if !r.Match(errors.E(errors.NotExist)) {
		t.Fatal(r.Diag())
	}

	// Put the reader into (and take the owner out of) the dir access file.
	// The owner should still see the access file (since they own it).
	r.As(ownerName)
	r.Put(dirAccess, "*:"+readerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(dirAccess) {
		t.Fatal(r.Diag())
	}
	r.As(readerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(dirAccess) {
		t.Fatal(r.Diag())
	}

	// Do the same, but for the base access file.
	r.As(ownerName)
	r.Delete(dirAccess)
	r.Put(baseAccess, "*:"+readerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(baseAccess) {
		t.Fatal(r.Diag())
	}
	r.As(readerName)
	r.DirWhichAccess(file)
	if !r.GotEntry(baseAccess) {
		t.Fatal(r.Diag())
	}
}

// TODO
func testWhichAccessLinkErrors(t *testing.T, r *testenv.Runner) {}
func testGlobErrors(t *testing.T, r *testenv.Runner)            {}
func testGlobLinkErrors(t *testing.T, r *testenv.Runner)        {}
