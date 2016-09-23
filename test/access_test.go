// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"

	_ "upspin.io/dir/unassigned"
)

// TODO(adg, r): use testenv.Runner to run these tests,
// instead of the ad hoc 'runner' type.

// Arguments for errStr in helpers.
const (
	success  = ""
	notExist = "does not exist"
)

type runner struct {
	state      string
	env        *testenv.Env
	owner      upspin.UserName
	userClient upspin.Client
	t          *testing.T
}

func (r *runner) read(user upspin.UserName, file upspin.PathName, errStr string) {
	file = path.Join(upspin.PathName(r.owner), string(file))
	var err error
	client := r.env.Client
	if user != r.owner {
		client = r.userClient
	}
	_, err = client.Get(file)
	r.check("Get", user, file, err, errStr)
}

func (r *runner) whichAccess(user upspin.UserName, file upspin.PathName, errStr string) {
	file = path.Join(upspin.PathName(r.owner), string(file))
	var err error
	client := r.env.Client
	if user != r.owner {
		client = r.userClient
	}
	dir, err := client.DirServer(file)
	if err != nil {
		r.Errorf("WhichAccess: cannot get DirServer for file %q: %v", file, err)
		return
	}
	_, err = dir.WhichAccess(file)
	r.check("WhichAccess", user, file, err, errStr)
}

func (r *runner) check(op string, user upspin.UserName, file upspin.PathName, err error, errStr string) {
	if errStr == "" {
		if err != nil {
			r.Errorf("%s: %s %q for user %q failed incorrectly: %v", r.state, op, file, user, err)
		}
	} else if err == nil {
		r.Errorf("%s: %s %q for user %q succeeded incorrectly: %v", r.state, op, file, user, err)
	} else if s := err.Error(); !strings.Contains(s, errStr) {
		r.Errorf("%s: %s %q for user %q failed with error:\n\t%v\nwant:\n\t%v", r.state, op, file, user, err, errStr)
	}
}

func (r *runner) Errorf(format string, args ...interface{}) {
	_, file, line, ok := runtime.Caller(3)
	if ok { // Should never fail.
		if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
			file = file[slash+1:]
		}
		format = fmt.Sprintf("%s:%d: ", file, line) + format
	}
	r.t.Errorf(format, args...)
}

func (r *runner) write(user upspin.UserName, file upspin.PathName, contents string, errStr string) {
	file = path.Join(upspin.PathName(r.owner), string(file))
	var err error
	client := r.env.Client
	if user != r.owner {
		client = r.userClient
	}
	_, err = client.Put(file, []byte(contents))
	r.check("Put", user, file, err, errStr)
}

func testReadAccess(t *testing.T, r *testenv.Runner) {
	const (
		user              = readerName
		owner             = ownerName
		base              = owner + "/"
		groupDir          = base + "Group"
		publicDir         = base + "public"
		privateDir        = base + "private"
		publicFile        = publicDir + "/public.txt"
		privateFile       = privateDir + "/private.txt"
		contentsOfPublic  = "public file"
		contentsOfPrivate = "private file"
	)

	// Build test tree.
	r.As(owner)
	r.MakeDirectory(groupDir)
	r.MakeDirectory(publicDir)
	r.Put(publicFile, contentsOfPublic)
	r.MakeDirectory(privateDir)
	r.Put(privateFile, contentsOfPrivate)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// With no access files, every item is readable by owner.
	r.Get(privateFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfPrivate {
		t.Errorf("data = %q, want = %q", r.Data, contentsOfPrivate)
	}
	r.Get(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfPublic {
		t.Errorf("data = %q, want = %q", r.Data, contentsOfPublic)
	}

	// With no access files, no item is visible to user.
	r.As(user)
	r.DirLookup(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(publicDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(publicFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Add /public/Access, granting Read to user and write to owner.
	const accessFile = publicDir + "/Access"
	var (
		accessText = fmt.Sprintf("r:%s\nw:%s", user, owner)
	)
	r.As(owner)
	r.Put(accessFile, accessText)
	r.Put(publicFile, contentsOfPublic) // Put again to ensure re-wrapping of keys. TODO: fix.

	// With Access file, every item is still readable by owner.
	r.Get(privateFile)
	r.Get(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// With Access file, only public items are visible to user.
	r.As(user)
	r.DirLookup(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(publicDir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.Get(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfPublic {
		t.Errorf("data = %q, want = %q", r.Data, contentsOfPublic)
	}

	// Change Access file to disable again.
	const (
		noUserAccessText = "r: someoneElse@test.com\n"
	)
	r.As(owner)
	r.Put(accessFile, noUserAccessText)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.As(user)
	r.DirLookup(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(publicDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(publicFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Put(publicFile, "will not succeed")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Now create a group and put user in it and make owner a writer.
	const groupFile = groupDir + "/mygroup"
	var (
		groupAccessText = string("r: mygroup\nw:" + owner)
		groupText       = fmt.Sprintf("%s\n", user)
	)

	r.As(owner)
	r.Put(groupFile, groupText)
	r.Put(accessFile, groupAccessText)
	r.Put(publicFile, contentsOfPublic) // Put file again to trigger sharing.
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	r.As(user)
	r.DirLookup(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(publicDir)
	r.Get(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfPublic {
		t.Errorf("data = %q, want = %q", r.Data, contentsOfPublic)
	}

	// Remove Group file and check user lost all access now.
	r.As(owner)
	r.Delete(groupFile)

	r.As(user)
	r.DirLookup(publicDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(publicFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Put group file back, but take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)

	r.As(owner)
	r.Put(groupFile, noUserGroupText)

	r.As(user)
	r.DirLookup(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirLookup(publicDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.Get(publicFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Remove group file.
	r.As(owner)
	r.Delete(groupFile)
}

func testWhichAccess(t *testing.T, r *testenv.Runner) {
	const (
		user              = readerName
		owner             = ownerName
		base              = owner + "/which-access"
		publicDir         = base + "/public"
		privateDir        = base + "/private"
		publicFile        = publicDir + "/public.txt"
		privateFile       = privateDir + "/private.txt"
		contentsOfPublic  = "public file"
		contentsOfPrivate = "private file"
	)
	r.As(owner)
	r.MakeDirectory(base)
	r.MakeDirectory(publicDir)
	r.Put(publicFile, contentsOfPublic)
	r.MakeDirectory(privateDir)
	r.Put(privateFile, contentsOfPrivate)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// With no access files, every item is seen by owner.
	r.DirWhichAccess(base)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(privateDir)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(privateFile)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(publicDir)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(publicFile)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// With no access files, no item is seen by user.
	r.As(user)
	r.DirWhichAccess(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(publicDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(publicFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// Add /public/Access, granting List to user.
	var (
		accessFile = upspin.PathName(publicDir + "/Access")
		accessText = fmt.Sprintf("list:%s\nw:%s", user, owner)
	)
	r.As(owner)
	r.Put(accessFile, accessText)
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// With Access file, every item is still seen by owner.
	r.DirWhichAccess(base)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(privateDir)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(privateFile)
	if r.Entry != nil {
		t.Errorf("entry.Name = %q, want = nil", r.Entry.Name)
	}
	r.DirWhichAccess(publicDir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if got, want := r.Entry.Name, accessFile; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}
	r.DirWhichAccess(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if got, want := r.Entry.Name, accessFile; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}

	// With Access file, only public items are seen by user.
	r.As(user)
	r.DirWhichAccess(base)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(privateDir)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(privateFile)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.DirWhichAccess(publicDir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if got, want := r.Entry.Name, accessFile; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}
	r.DirWhichAccess(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if got, want := r.Entry.Name, accessFile; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}
}
