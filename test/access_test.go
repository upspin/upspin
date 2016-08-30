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

// Arguments for errStr in helpers.
const (
	success    = ""
	permission = "permission denied"
	notExist   = "does not exist"
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

func testReadAccess(t *testing.T, packing upspin.Packing) {
	var (
		user  = newUserName()
		owner = newUserName()
	)
	const (
		groupDir         = "Group"
		publicDir        = "public"
		privateDir       = "private"
		publicFile       = publicDir + "/public.txt"
		privateFile      = privateDir + "/private.txt"
		contentsOfPublic = "public file"
	)
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Kind:      "inprocess",
		Tree: testenv.Tree{
			testenv.E(groupDir+"/", success),
			testenv.E(publicDir+"/", success),
			testenv.E(publicFile, contentsOfPublic),
			testenv.E(privateDir+"/", success),
			testenv.E(privateFile, "private"),
		},
	}

	env, err := testenv.New(testSetup)
	if err != nil {
		t.Fatal(err)
	}

	userClient, _, err := env.NewUser(user)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}

	r := runner{
		env:        env,
		owner:      owner,
		userClient: userClient,
		t:          t,
	}

	// With no access files, every item is readable by owner.
	r.state = "No Access files"
	r.read(owner, privateFile, success)
	r.read(owner, publicFile, success)

	// With no access files, no item is visible to user.
	r.read(user, "", notExist)
	r.read(user, privateDir, notExist)
	r.read(user, privateDir, notExist)
	r.read(user, publicDir, notExist)
	r.read(user, publicFile, notExist)

	// Add /public/Access, granting Read to user and write to owner.
	const accessFile = "/public/Access"
	var (
		accessText = fmt.Sprintf("r:%s\nw:%s", user, owner)
	)
	r.state = "With Access file"
	r.write(owner, accessFile, accessText, success)

	// With Access file, every item is still readable by owner.
	r.read(owner, privateFile, success)
	r.read(owner, publicFile, success)

	// With Access file, only public items are visible to user.
	r.read(user, "", notExist)
	r.read(user, privateDir, notExist)
	r.read(user, privateDir, notExist)
	// r.read(user, publicFile, success) TODO: Unpack: could not find wrapped key

	// The only way to update the keys for the file using the Client interface is to use Put,
	// which will call packer.Share. That also stores the file again, which is unnecessary. TODO.
	r.write(owner, publicFile, contentsOfPublic, success)
	r.read(user, publicFile, success)

	// Change Access file to disable again.
	const (
		noUserAccessText = "r: someoneElse@test.com\n"
	)
	r.state = "With no user in Access file"
	r.write(owner, accessFile, noUserAccessText, success)

	r.read(user, "", notExist)
	r.read(user, privateDir, notExist)
	r.read(user, privateDir, notExist)
	r.read(user, publicDir, notExist)
	r.read(user, publicFile, notExist)
	r.write(user, publicFile, "will not succeed", notExist)

	// Now create a group and put user in it and make owner a writer.
	const groupFile = "/Group/mygroup"
	var (
		groupAccessText = string("r: mygroup\nw:" + owner)
		groupText       = fmt.Sprintf("%s\n", user)
	)
	r.state = "With user in Group file"
	r.write(owner, accessFile, groupAccessText, success)
	r.write(owner, groupFile, groupText, success)

	r.read(user, "", notExist)
	r.read(user, privateDir, notExist)
	r.read(user, privateDir, notExist)
	r.read(user, publicFile, success)

	r.write(owner, publicFile, contentsOfPublic, success) // Put file again to trigger sharing.
	r.read(user, publicFile, success)

	// Take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)
	r.state = "With no user in Group file"
	r.write(owner, groupFile, noUserGroupText, success)

	r.read(user, "", notExist)
	r.read(user, privateDir, notExist)
	r.read(user, privateDir, notExist)
	r.read(user, publicDir, notExist)
	r.read(user, publicFile, notExist)
}

func testWhichAccess(t *testing.T, packing upspin.Packing) {
	var (
		user  = newUserName()
		owner = newUserName()
	)
	const (
		publicDir        = "public"
		privateDir       = "private"
		publicFile       = publicDir + "/public.txt"
		privateFile      = privateDir + "/private.txt"
		contentsOfPublic = "public file"
	)
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Kind:      "inprocess",
		Tree: testenv.Tree{
			testenv.E(publicDir+"/", success),
			testenv.E(publicFile, contentsOfPublic),
			testenv.E(privateDir+"/", success),
			testenv.E(privateFile, "private"),
		},
	}

	env, err := testenv.New(testSetup)
	if err != nil {
		t.Fatal(err)
	}

	userClient, _, err := env.NewUser(user)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}

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
