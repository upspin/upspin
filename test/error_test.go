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
	if !r.Match(errNotExist) {
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
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Give the reader a specific (non-read) right and
	// it should see a permission error.
	for _, right := range []string{"list", "write", "create", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+readerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(readerName)
		r.Get(file)
		if !r.Match(errPermission) {
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
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(link)
	if !r.Match(errNotExist) {
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
	if !r.Match(errors.E(errors.NotExist, upspin.PathName(file))) {
		t.Fatal(r.Diag())
	}

	// Add a non-read permission for the target,
	// now a get of the link should fail with 'permission' for the target.
	r.As(ownerName)
	r.Put(dstAccess, "list:"+readerName)
	r.As(readerName)
	r.Get(link)
	if !r.Match(errors.E(errors.Permission, upspin.PathName(file))) {
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
	if !r.Match(errNotExist) {
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
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Give the writer a specific (non-write/create) right and
	// it should see a permission error.
	for _, right := range []string{"list", "read", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+writerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(writerName)
		r.Put(file, content)
		if !r.Match(errPermission) {
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
	if !r.Match(errPermission) {
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
	if !r.Match(errPermission) {
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

func testPutLinkErrors(t *testing.T, r *testenv.Runner) {
	// TODO(r): fix issue 53
	// t.Skip("broken until issue #53 is fixed")

	const (
		base      = ownerName + "/put-link-errors"
		srcDir    = base + "/src"
		dstDir    = base + "/dst"
		srcAccess = srcDir + "/Access"
		dstAccess = dstDir + "/Access"
		file      = dstDir + "/file"

		fileLink            = srcDir + "/file-link"
		fileTarget          = file
		fileThroughFileLink = fileLink

		dirLink            = srcDir + "/dir-link"
		dirTarget          = dstDir
		fileThroughDirLink = dirLink + "/file"

		content = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(srcDir)
	r.MakeDirectory(dstDir)
	r.PutLink(fileTarget, fileLink)
	r.PutLink(dirTarget, dirLink)
	r.Put(fileThroughFileLink, content)
	r.Get(fileTarget)
	r.Delete(fileTarget)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != content {
		t.Fatalf("link content = %q, want %q", r.Data, content)
	}
	r.Put(fileThroughDirLink, content)
	r.Get(fileTarget)
	r.Delete(fileTarget)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != content {
		t.Fatalf("link content = %q, want %q", r.Data, content)
	}

	cases := []struct {
		op                    string
		link, fileThroughLink upspin.PathName
	}{
		{"file link:", fileLink, fileThroughFileLink},
		{"dir link:", dirLink, fileThroughDirLink},
	}
	for _, c := range cases {
		// As a user with no rights over the link,
		// we should get a 'not exist' error.
		r.As(writerName)
		r.Put(c.fileThroughLink, content)
		if !r.Match(errNotExist) {
			t.Fatal(c.op, r.Diag())
		}

		// Give the user rights over the destination file
		// but still no rights over the link. The user should
		// not be able to put the file, because the link is hidden.
		r.As(ownerName)
		r.Put(dstAccess, "*:"+ownerName+"\n*:"+writerName)
		r.As(writerName)
		r.Put(file, content)
		r.Delete(file)
		if r.Failed() {
			t.Fatal(c.op, r.Diag())
		}
		r.Put(c.fileThroughLink, content)
		if !r.Match(errors.E(errors.NotExist, upspin.PathName(c.link))) {
			t.Fatal(c.op, r.Diag())
		}

		// Give the user one right over the link and Put should work
		// as the client will now be able to follow the link.
		for _, right := range []string{"list", "create", "write", "read", "delete"} {
			r.As(ownerName)
			r.Put(srcAccess, right+":"+writerName)
			r.As(writerName)
			r.Put(c.fileThroughLink, content)
			r.Delete(file)
			if r.Failed() {
				t.Fatalf("right = %q: %s", right, r.Diag())
			}
		}

		// Remove the user's rights to the file, now a put through the
		// link should fail with 'not exist' for the file.
		r.As(ownerName)
		r.Put(srcAccess, "list:"+writerName)
		r.Delete(dstAccess)
		r.As(writerName)
		r.Put(c.fileThroughLink, content)
		if !r.Match(errors.E(errors.NotExist, upspin.PathName(file))) {
			t.Fatal(c.op, r.Diag())
		}

		// Add a non-read permission for the file,
		// now a put of the link should fail with 'permission' for the file.
		r.As(ownerName)
		r.Put(dstAccess, "*:"+ownerName+"\nlist:"+writerName)
		r.As(writerName)
		r.Put(c.fileThroughLink, content)
		if !r.Match(errors.E(errors.Permission, upspin.PathName(file))) {
			t.Fatal(c.op, r.Diag())
		}

		// Clean up before next loop iteration.
		r.As(ownerName)
		r.Delete(srcAccess)
		r.Delete(dstAccess)
		if r.Failed() {
			t.Fatal(c.op, r.Diag())
		}
	}
}

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
	if !r.Match(errNotExist) {
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
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Give the writer a specific (non-create) right and
	// it should see a permission error.
	for _, right := range []string{"list", "read", "write", "delete"} {
		r.As(ownerName)
		r.Put(access, right+":"+writerName)
		if r.Failed() {
			t.Fatal(r.Diag())
		}
		r.As(writerName)
		r.MakeDirectory(subdir)
		if !r.Match(errPermission) {
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
	if !r.Match(errExist) {
		t.Fatal(r.Diag())
	}

	// Lookup should fail as the writer.
	r.DirLookup(subdir)
	if !r.Match(errPermission) {
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

func testMakeDirectoryLinkErrors(t *testing.T, r *testenv.Runner) {
	const (
		base      = ownerName + "/makedirectory-link-errors"
		srcDir    = base + "/src"
		dstDir    = base + "/dst"
		srcAccess = srcDir + "/Access"
		dstAccess = dstDir + "/Access"
		dir       = dstDir + "/dir"

		directLink           = srcDir + "/direct-link"
		directTarget         = dir
		dirThroughDirectLink = directLink

		indirectLink           = srcDir + "/indirect-link"
		indirectTarget         = dstDir
		dirThroughIndirectLink = indirectLink + "/dir"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(srcDir)
	r.MakeDirectory(dstDir)
	r.PutLink(directTarget, directLink)
	r.PutLink(indirectTarget, indirectLink)
	r.MakeDirectory(dirThroughDirectLink)
	r.Delete(dir)
	r.MakeDirectory(dirThroughIndirectLink)
	r.Delete(dir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	cases := []struct {
		op                   string
		link, dirThroughLink upspin.PathName
	}{
		{"direct link:", directLink, dirThroughDirectLink},
		{"indirect link:", indirectLink, dirThroughIndirectLink},
	}
	for _, c := range cases {
		// As a user with no rights over the link,
		// we should get a 'not exist' error.
		r.As(writerName)
		r.MakeDirectory(c.dirThroughLink)
		if !r.Match(errNotExist) {
			t.Fatal(c.op, r.Diag())
		}

		// Give the user rights over the destination directory
		// but still no rights over the link. The user should
		// not be able to make the directory, because the link is hidden.
		r.As(ownerName)
		r.Put(dstAccess, "*:"+ownerName+"\n*:"+writerName)
		r.As(writerName)
		r.MakeDirectory(dir)
		r.Delete(dir)
		if r.Failed() {
			t.Fatal(c.op, r.Diag())
		}
		r.MakeDirectory(c.dirThroughLink)
		if !r.Match(errors.E(errors.NotExist, upspin.PathName(c.link))) {
			t.Fatal(c.op, r.Diag())
		}

		// Give the user one right over the link and MakeDirectory should work
		// as the client will now be able to follow the link.
		for _, right := range []string{"list", "create", "write", "read", "delete"} {
			r.As(ownerName)
			r.Put(srcAccess, right+":"+writerName)
			r.As(writerName)
			r.MakeDirectory(c.dirThroughLink)
			r.Delete(dir)
			if r.Failed() {
				t.Fatalf("%s right = %q: %s", c.op, right, r.Diag())
			}
		}

		// Remove the user's rights to the directory, now a
		// MakeDirectory through the link should fail with 'not
		// exist' for the dir.
		r.As(ownerName)
		r.Put(srcAccess, "list:"+writerName)
		r.Delete(dstAccess)
		r.As(writerName)
		r.MakeDirectory(c.dirThroughLink)
		if !r.Match(errors.E(errors.NotExist, upspin.PathName(dir))) {
			t.Fatal(c.op, r.Diag())
		}

		// Add a non-read permission for the directory,
		// now a mkdir of the link should fail with 'permission' for the dir.
		r.As(ownerName)
		r.Put(dstAccess, "*:"+ownerName+"\nlist:"+writerName)
		r.As(writerName)
		r.MakeDirectory(c.dirThroughLink)
		if !r.Match(errors.E(errors.Permission, upspin.PathName(dir))) {
			t.Fatal(c.op, r.Diag())
		}

		// Clean up before next loop iteration.
		r.As(ownerName)
		r.Delete(srcAccess)
		r.Delete(dstAccess)
		if r.Failed() {
			t.Fatal(c.op, r.Diag())
		}
	}
}

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
	if !r.Match(errNotExist) {
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
	if !r.Match(errNotExist) {
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
	if !r.Match(errNotExist) {
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

func testGlobErrors(t *testing.T, r *testenv.Runner) {
	const (
		base       = ownerName + "/glob-errors"
		dir        = base + "/dir"
		baseFile   = base + "/file"
		dirFile    = dir + "/file"
		baseAccess = base + "/Access"
		dirAccess  = dir + "/Access"
		content    = "hello, gophers"
	)
	r.As(ownerName)
	r.MakeDirectory(base)
	r.MakeDirectory(dir)
	r.Put(baseFile, content)
	r.Put(dirFile, content)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Owner should be able to glob them all.
	r.Glob(base + "/*")
	if !r.GotEntries(true, dir, baseFile) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.GotEntries(true, dirFile) {
		t.Fatal(r.Diag())
	}

	// The reader should get an error
	// because they can't see the base of the glob.
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Give a reader list rights and they can see
	// all files but no block data.
	r.As(ownerName)
	r.Put(baseAccess, "list:"+readerName)
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.GotEntries(false, baseAccess, dir, baseFile) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.GotEntries(false, dirFile) {
		t.Fatal(r.Diag())
	}

	// With read rights they can see everything.
	r.As(ownerName)
	r.Put(baseAccess, "list,read:"+readerName)
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.GotEntries(true, baseAccess, dir, baseFile) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.GotEntries(true, dirFile) {
		t.Fatal(r.Diag())
	}

	// Deny the reader access to the dir,
	// they can still see the root but not the dir.
	r.As(ownerName)
	r.Put(dirAccess, "*:"+ownerName)
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.GotEntries(true, baseAccess, dir, baseFile) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.GotEntries(false) {
		t.Fatal(r.Diag())
	}

	// Give the reader list access to the dir, they can still see the root
	// (with blocks) and the dir (without blocks).
	r.As(ownerName)
	r.Put(dirAccess, "list:"+readerName)
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.GotEntries(true, baseAccess, dir, baseFile) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.GotEntries(false, dirAccess, dirFile) {
		t.Fatal(r.Diag())
	}

	// Remove the root access file and the reader should
	// still be able to glob the directory, given its name.
	r.As(ownerName)
	r.Delete(baseAccess)
	r.As(readerName)
	r.Glob(base + "/*")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Glob(base + "/*/*")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Glob(dir + "/*")
	if !r.GotEntries(false, dirAccess, dirFile) {
		t.Fatal(r.Diag())
	}

	// Now add a second directory, and give the reader list and read rights
	// to it, and also give them list rights to the root. With one glob file
	// that matches both directories, the reader should the file in dir with
	// no blocks, and the file in dir2 with blocks.
	const (
		dir2       = base + "/dir2"
		dir2File   = dir2 + "/file"
		dir2Access = dir2 + "/Access"
	)
	r.As(ownerName)
	r.Put(baseAccess, "*:"+ownerName+"\nlist:"+readerName)
	r.MakeDirectory(dir2)
	r.Put(dir2File, content)
	r.Put(dir2Access, "list,read:"+readerName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.As(readerName)
	r.Glob(base + "/*/f*")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if n := len(r.Entries); n != 2 {
		t.Fatalf("got %d files, want %d", n, 2)
	}
	if e := r.Entries[0]; e.Name != dirFile || len(e.Blocks) != 0 {
		t.Fatalf("got %q with %d blocks, want %q with 0 blocks", e.Name, len(e.Blocks), dirFile)
	}
	if e := r.Entries[1]; e.Name != dir2File || len(e.Blocks) == 0 {
		t.Fatalf("got %q with 0 blocks, want %q with blocks", e.Name, dirFile)
	}
}

// TODO
func testGlobLinkErrors(t *testing.T, r *testenv.Runner) {}
