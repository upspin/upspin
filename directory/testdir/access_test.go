package testdir_test

import (
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"
)

type runner struct {
	state      string
	env        *testenv.Env
	owner      upspin.UserName
	userClient upspin.Client
	t          *testing.T
}

func (r *runner) read(user upspin.UserName, file upspin.PathName, shouldSucceed bool) {
	file = upspin.PathName(r.owner) + "/" + file
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
			r.t.Errorf("%s: %s %q for user %q failed incorrectly: %v", r.state, op, file, user, err)
		}
	} else {
		if err == nil {
			r.t.Errorf("%s: %s %q for user %q succeeded incorrectly: %v", r.state, op, file, user, err)
		} else if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
			r.t.Errorf("%s: %s %q for user %q failed with wrong error: %v", r.state, op, file, user, err)
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
		Keys: upspin.KeyPair{
			Public:  upspin.PublicKey("public key"),
			Private: upspin.PrivateKey("public key"),
		},
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

	userClient, err := env.NewUser(user, nil)
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
	r.read(user, publicFile, true)

	// Change Access file to disable again.
	const (
		noUserAccessText = "r: someoneElse@test.com\n"
	)
	_, err = env.Client.Put(accessFile, []byte(noUserAccessText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With no user in Access file"

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, false)
	r.read(user, publicFile, false)

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

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, true)
	r.read(user, publicFile, true)

	// Take user out of the group.
	const (
		noUserGroupText = "someoneElse@test.com\n"
	)
	_, err = env.Client.Put(groupFile, []byte(noUserGroupText))
	if err != nil {
		t.Fatal(err)
	}
	r.state = "With no user in Group file"

	r.read(user, "", false)
	r.read(user, privateDir, false)
	r.read(user, privateDir, false)
	r.read(user, publicDir, false)
	r.read(user, publicFile, false)
}
