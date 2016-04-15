package testdir_test

import (
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"
)

type runner struct {
	state string
	env   *testenv.Env
	owner upspin.UserName
	t     *testing.T
}

func (r *runner) reset() {
	r.env.Context.UserName = r.owner // TODO: Do this better. Ugly hack.
}

func (r *runner) lookup(user upspin.UserName, file upspin.PathName, shouldSucceed bool) {
	file = upspin.PathName(r.owner) + "/" + file
	r.env.Context.UserName = user // TODO: Do this better. Ugly hack.
	defer r.reset()
	entry, err := r.env.Context.Directory.Lookup(file)
	r.check("Lookup", file, err, shouldSucceed)
	if err == nil && entry.Name != file {
		r.t.Fatalf("%s: lookup got entry %q; arg was %q", r.state, entry.Name, file)
	}
}

func (r *runner) check(op string, file upspin.PathName, err error, shouldSucceed bool) {
	if shouldSucceed {
		if err != nil {
			r.t.Errorf("%s: %s %q for user %q failed incorrectly: %v", r.state, op, file, r.env.Context.UserName, err)
		}
	} else {
		if err == nil {
			r.t.Errorf("%s: %s %q for user %q succeeded incorrectly: %v", r.state, op, file, r.env.Context.UserName, err)
		} else if !strings.Contains(err.Error(), "permission denied") {
			r.t.Errorf("%s: %s %q for user %q failed with wrong error: %v", r.state, op, file, r.env.Context.UserName, err)
		}
	}
}

func TestReadAccess(t *testing.T) {
	const (
		owner       = "owner1@test.com"
		user        = "user1@test.com"
		groupDir    = "Group"
		publicDir   = "public"
		privateDir  = "private"
		publicFile  = publicDir + "/public.txt"
		privateFile = privateDir + "/private.txt"
	)
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   upspin.DebugPack,
		Transport: upspin.InProcess,
		Tree: testenv.Tree{
			testenv.E(groupDir+"/", ""),
			testenv.E(publicDir+"/", ""),
			testenv.E(publicFile, "public"),
			testenv.E(privateDir+"/", ""),
			testenv.E(privateFile, "private"),
		},
	}

	env, err := testenv.New(testSetup)
	if err != nil {
		t.Fatal(err)
	}

	r := runner{
		env:   env,
		owner: owner,
		t:     t,
	}

	// With no access files, every item is readable by owner.
	r.state = "No Access files"
	r.lookup(owner, "", true)
	r.lookup(owner, privateDir, true)
	r.lookup(owner, privateDir, true)
	r.lookup(owner, publicDir, true)
	r.lookup(owner, publicFile, true)

	// With no access files, no item is readable by user.
	r.lookup(user, "", false)
	r.lookup(user, privateDir, false)
	r.lookup(user, privateDir, false)
	r.lookup(user, publicDir, false)
	r.lookup(user, publicFile, false)

	// Add /private/Access, granting Read to user.
	const (
		accessFile = owner + "/public/Access"
		accessText = "r: user1@test.com\n"
	)
	_, err = env.Client.Put(accessFile, []byte(accessText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With Access file"

	// With Access file, every item is still readable by owner.
	r.lookup(owner, "", true)
	r.lookup(owner, privateDir, true)
	r.lookup(owner, privateDir, true)
	r.lookup(owner, publicDir, true)
	r.lookup(owner, publicFile, true)

	// With Access file, only public items are readable by user.
	r.lookup(user, "", false)
	r.lookup(user, privateDir, false)
	r.lookup(user, privateDir, false)
	r.lookup(user, publicDir, true)
	r.lookup(user, publicFile, true)

	// Change Access file to disable again.
	const (
		noUserAccessText = "r: someoneElse@test.com\n"
	)
	_, err = env.Client.Put(accessFile, []byte(noUserAccessText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With no user in Access file"

	r.lookup(user, "", false)
	r.lookup(user, privateDir, false)
	r.lookup(user, privateDir, false)
	r.lookup(user, publicDir, false)
	r.lookup(user, publicFile, false)

	// Now create a group and put user in it.
	const (
		groupFile       = owner + "/Group/mygroup"
		groupText       = "user1@test.com\n"
		groupAccessText = "r: mygroup\n"
	)
	_, err = env.Client.Put(groupFile, []byte(groupText))
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.Client.Put(accessFile, []byte(groupAccessText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With user in Group file"

	r.lookup(user, "", false)
	r.lookup(user, privateDir, false)
	r.lookup(user, privateDir, false)
	r.lookup(user, publicDir, true)
	r.lookup(user, publicFile, true)

	// Take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)
	_, err = env.Client.Put(groupFile, []byte(noUserGroupText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With no user in Group file"

	r.lookup(user, "", false)
	r.lookup(user, privateDir, false)
	r.lookup(user, privateDir, false)
	r.lookup(user, publicDir, false)
	r.lookup(user, publicFile, false)
}
