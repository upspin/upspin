// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

// Arguments for errStr in helpers.
const (
	success    = ""
	permission = "permission denied"
	notExist   = "does not exist"
)

var (
	ownersKey    = keyStore[ownersName]["p256"]
	readersKey   = keyStore[readersName]["p256"]
	ownersKey521 = keyStore[ownersName]["p521"]
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
		r.Errorf("%s: %s %q for user %q failed with error: %q, want error %q", r.state, op, file, user, err, errStr)
	}
}

func (r *runner) Errorf(format string, args ...interface{}) {
	_, file, line, ok := runtime.Caller(3)
	if ok { // Should never fail.
		if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
			file = file[slash+1:]
		}
		r.t.Errorf("called from %s:%d with %s packing:", file, line, pack.Lookup(r.env.Context.Packing()))
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
	key := ownersKey
	// TODO  try different key types
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Transport: upspin.InProcess,
		Keys:      key,
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

	userClient, _, err := env.NewUser(user, nil)
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
	r.read(owner, "", success)
	r.read(owner, privateDir, success)
	r.read(owner, privateDir, success)
	r.read(owner, publicDir, success)
	r.read(owner, publicFile, success)

	// With no access files, no item is readable by user.
	r.read(user, "", permission)
	r.read(user, privateDir, permission)
	r.read(user, privateDir, permission)
	r.read(user, publicDir, permission)
	r.read(user, publicFile, permission)

	// Add /public/Access, granting Read to user and write to owner.
	const accessFile = "/public/Access"
	var (
		accessText = fmt.Sprintf("r:%s\nw:%s", user, owner)
	)
	r.state = "With Access file"
	r.write(owner, accessFile, accessText, success)

	// With Access file, every item is still readable by owner.
	r.read(owner, "", success)
	r.read(owner, privateDir, success)
	r.read(owner, privateDir, success)
	r.read(owner, publicDir, success)
	r.read(owner, publicFile, success)

	// With Access file, only public items are readable by user.
	r.read(user, "", permission)
	r.read(user, privateDir, permission)
	r.read(user, privateDir, permission)
	r.read(user, publicDir, success)

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

	r.read(user, "", permission)
	r.read(user, privateDir, permission)
	r.read(user, privateDir, permission)
	r.read(user, publicDir, permission)
	r.read(user, publicFile, permission)
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

	r.read(user, "", permission)
	r.read(user, privateDir, permission)
	r.read(user, privateDir, permission)
	r.read(user, publicDir, success)

	r.write(owner, publicFile, contentsOfPublic, success) // Put file again to trigger sharing.
	r.read(user, publicFile, success)

	// Take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)
	r.state = "With no user in Group file"
	r.write(owner, groupFile, noUserGroupText, success)

	r.read(user, "", permission)
	r.read(user, privateDir, permission)
	r.read(user, privateDir, permission)
	r.read(user, publicDir, permission)
	r.read(user, publicFile, permission)
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
	key := ownersKey
	// TODO  try different key types
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Transport: upspin.InProcess,
		Keys:      key,
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

	userClient, _, err := env.NewUser(user, nil)
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
