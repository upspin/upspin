package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"
)

// p256 keys
var (
	ownersKey = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}

	readersKey = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n71924488370379946818987229050653820040970171638729570985826507440590282890744\n98209510739612452175889722244428941929387851511059412496741067489708636153322"),
		Private: upspin.PrivateKey("12667847114690182845907216480350218830765432137334449282204959715092837120411"),
	}

	unauthorizedKey = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n451297374904948634374054356512870959086357020197658801395547428912730444027855\n5208130801397165557035966850900120994093972759687728686978325024298897500727"),
		Private: upspin.PrivateKey("83500684821364595639775395247901350218614453487586824492362672933425261154632"),
	}
)

// p521 keys
var (
	ownersKey521 = upspin.KeyPair{
		Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
		Private: upspin.PrivateKey("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
	}
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

func testReadAccess(t *testing.T, packing upspin.Packing) {
	var (
		user  = newUserName()
		owner = newUserName()
	)
	const (
		groupDir    = "Group"
		publicDir   = "public"
		privateDir  = "private"
		publicFile  = publicDir + "/public.txt"
		privateFile = privateDir + "/private.txt"
	)
	key := ownersKey
	if packing == upspin.EEp521Pack {
		key = ownersKey521
	}
	testSetup := &testenv.Setup{
		OwnerName: upspin.UserName(owner),
		Packing:   packing,
		Transport: upspin.InProcess,
		Keys:      key,
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
	var (
		accessFile = upspin.PathName(owner + "/public/Access")
		accessText = fmt.Sprintf("r:%s\n", user)
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
	// r.read(user, publicFile, true) TODO: Cannot work until sharing is implemented.

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
	const groupAccessText = "r: mygroup\n"
	var (
		groupFile = upspin.PathName(owner + "/Group/mygroup")
		groupText = fmt.Sprintf("%s\n", user)
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
	// r.read(user, publicFile, true) TODO: Cannot work until sharing is implemented.

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
