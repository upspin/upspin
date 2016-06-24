// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"upspin.io/access"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"
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

func (r *runner) read(user upspin.UserName, file upspin.PathName, shouldSucceed bool) {
	file = path.Join(upspin.PathName(r.owner), string(file))
	var err error
	client := r.env.Client
	if user != r.owner {
		client = r.userClient
	}
	_, err = client.Get(file)
	r.check("Get", user, file, err, shouldSucceed)
}

func (r *runner) check(op string, user upspin.UserName, file upspin.PathName, err error, shouldSucceed bool) {
	if shouldSucceed {
		if err != nil {
			r.Errorf("%s: %s %q for user %q failed incorrectly: %v", r.state, op, file, user, err)
		}
	} else {
		if err == nil {
			r.Errorf("%s: %s %q for user %q succeeded incorrectly: %v", r.state, op, file, user, err)
		} else if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
			r.Errorf("%s: %s %q for user %q failed with wrong error: %v", r.state, op, file, user, err)
		}
	}
}

func (r *runner) Errorf(format string, args ...interface{}) {
	_, file, line, ok := runtime.Caller(3)
	if ok { // Should never fail.
		if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
			file = file[slash+1:]
		}
		r.t.Errorf("called from %s:%d with %s packing:", file, line, pack.Lookup(r.env.Context.Packing))
	}
	r.t.Errorf(format, args...)
}

func (r *runner) write(user upspin.UserName, file upspin.PathName, contents string, shouldSucceed bool) {
	file = path.Join(upspin.PathName(r.owner), string(file))
	var err error
	client := r.env.Client
	if user != r.owner {
		client = r.userClient
	}
	_, err = client.Put(file, []byte(contents))
	r.check("Put", user, file, err, shouldSucceed)
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
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Transport: upspin.InProcess,
		Keys:      key,
		Tree: testenv.Tree{
			testenv.E(groupDir+"/", ""),
			testenv.E(publicDir+"/", ""),
			testenv.E(publicFile, contentsOfPublic),
			testenv.E(privateDir+"/", ""),
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
	r.read(owner, "", true)
	r.read(owner, privateDir, true)
	r.read(owner, privateDir, true)
	r.read(owner, publicDir, true)
	r.read(owner, publicFile, true)

	// With no access files, no item is readable by user.
	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, false)
	r.read(user, publicFile, false)

	// Add /public/Access, granting Read to user and write to owner.
	const accessFile = "/public/Access"
	var (
		accessText = fmt.Sprintf("r:%s\nw:%s", user, owner)
	)
	r.state = "With Access file"
	r.write(owner, accessFile, accessText, true)

	// With Access file, every item is still readable by owner.
	r.read(owner, "", true)
	r.read(owner, privateDir, true)
	r.read(owner, privateDir, true)
	r.read(owner, publicDir, true)
	r.read(owner, publicFile, true)

	// With Access file, only public items are readable by user.
	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, true)

	// The only way to update the keys for the file using the Client interface is to use Put,
	// which will call packer.Share. That also stores the file again, which is unnecessary. TODO.
	r.write(owner, publicFile, contentsOfPublic, true)
	r.read(user, publicFile, true)

	// Change Access file to disable again.
	const (
		noUserAccessText = "r: someoneElse@test.com\n"
	)
	r.state = "With no user in Access file"
	r.write(owner, accessFile, noUserAccessText, true)

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, false)
	r.read(user, publicFile, false)
	r.write(user, publicFile, "will not succeed", false)

	// Now create a group and put user in it and make owner a writer.
	const groupFile = "/Group/mygroup"
	var (
		groupAccessText = string("r: mygroup\nw:" + owner)
		groupText       = fmt.Sprintf("%s\n", user)
	)
	r.state = "With user in Group file"
	r.write(owner, accessFile, groupAccessText, true)
	r.write(owner, groupFile, groupText, true)

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, true)

	r.write(owner, publicFile, contentsOfPublic, true) // Put file again to trigger sharing.
	r.read(user, publicFile, true)

	// Take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)
	r.state = "With no user in Group file"
	r.write(owner, groupFile, noUserGroupText, true)

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, false)
	r.read(user, publicFile, false)
}
