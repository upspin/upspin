// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"upspin.io/client"
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
		user  = readerName
		owner = ownerName
		base = owner + "/"
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
	const groupFile = groupDir +"/mygroup"
	var (
		groupAccessText = string("r: mygroup\nw:" + owner)
		groupText       = fmt.Sprintf("%s\n", user)
	)

	r.As(owner)
	r.Put(accessFile, groupAccessText)
	r.Put(groupFile, groupText)
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

	r.As(owner)
	r.Put(publicFile, contentsOfPublic) // Put file again to trigger sharing.

	r.As(user)
	r.DirLookup(publicDir)
	r.Get(publicFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfPublic {
		t.Errorf("data = %q, want = %q", r.Data, contentsOfPublic)
	}

	// Take user out of the group.
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
}

func testWhichAccess(t *testing.T, packing upspin.Packing) {
	var (
		user  = newUserName()
		owner = newUserName()
		root  = upspin.PathName(owner) + "/"
	)
	const (
		publicDir        = "public"
		privateDir       = "private"
		publicFile       = publicDir + "/public.txt"
		privateFile      = privateDir + "/private.txt"
		contentsOfPublic = "public file"
	)
	testSetup := &testenv.Setup{
		OwnerName: owner,
		Packing:   packing,
		Kind:      "inprocess",
	}

	env, err := testenv.New(testSetup)
	if err != nil {
		t.Fatal(err)
	}

	// Build test tree.
	tr := testenv.NewRunner()
	tr.AddUser(env.Context)
	tr.As(owner)
	tr.MakeDirectory(root + publicDir)
	tr.Put(root+publicFile, contentsOfPublic)
	tr.MakeDirectory(root + privateDir)
	tr.Put(root+privateFile, "private")
	if tr.Failed() {
		t.Fatal(tr.Diag())
	}

	userContext, err := env.NewUser(user)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	userClient := client.New(userContext)

	r := runner{
		env:        env,
		owner:      owner,
		userClient: userClient,
		t:          t,
	}

	// With no access files, every item is seen by owner.
	r.state = "No Access files"
	r.whichAccess(owner, "", success)
	r.whichAccess(owner, privateDir, success)
	r.whichAccess(owner, privateDir, success)
	r.whichAccess(owner, publicDir, success)
	r.whichAccess(owner, publicFile, success)

	// With no access files, no item is seen by user.
	r.whichAccess(user, "", notExist)
	r.whichAccess(user, privateDir, notExist)
	r.whichAccess(user, privateDir, notExist)
	r.whichAccess(user, publicDir, notExist)
	r.whichAccess(user, publicFile, notExist)

	// Add /public/Access, granting List to user.
	const accessFile = "/public/Access"
	var (
		accessText = fmt.Sprintf("list:%s\nw:%s", user, owner)
	)
	r.state = "With Access file"
	r.write(owner, accessFile, accessText, success)

	// With Access file, every item is seen by owner.
	r.whichAccess(owner, "", success)
	r.whichAccess(owner, privateDir, success)
	r.whichAccess(owner, privateDir, success)
	r.whichAccess(owner, publicDir, success)
	r.whichAccess(owner, publicFile, success)

	// With Access file, only public items are seen by user.
	r.whichAccess(user, "", notExist)
	r.whichAccess(user, privateDir, notExist)
	r.whichAccess(user, privateDir, notExist)
	r.whichAccess(user, publicDir, success)
}
