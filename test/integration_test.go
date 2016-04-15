// Package test contains an integration test for all of Upspin.
// This particular integration test runs on GCP and as such we disable it
// from normal 'go test ./...' runs since it's too
// expensive. To run it, do 'go test -tags integration'
// TODO: move all or most of client/integration_test here.

// +build integration

package test

import (
	e "upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"
	"testing"
	"strings"
	"upspin.googlesource.com/upspin.git/access"
)

const (
	ownersName = "upspin-test@google.com"
	readerName = "upspin-friend-test@google.com"
	unauthorizedUser = "sally@unauthorized.com"
	contentsOfFile1 = "contents of file 1"
	contentsOfFile2 = "contents of file 2"
)

var (
	setup = e.Setup{
		Tree: e.Tree{
			e.E("/dir1/", ""),
			e.E("/dir2/", ""),
			e.E("/dir1/file1.txt", contentsOfFile1),
			e.E("/dir2/file2.txt", contentsOfFile2),
		},
		OwnerName: ownersName,
		Keys: ownersKey,
		Transport: upspin.GCP,
		IgnoreExistingDirectories: false,  // left-over Access files will be a problem.
		DeleteTreeAtExit: true,
	}

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


func testNoAccessFileNoOtherReadersAllowed(t *testing.T, env *e.Env) {
	client, err := env.NewUser(readerName, &readersKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(upspin.PathName(ownersName+"/dir1/file1.txt"))
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error contains %s, got %s", access.ErrPermissionDenied, err)
	}
}

func TestAll(t *testing.T) {
	env, err := e.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	testNoAccessFileNoOtherReadersAllowed(t, env)

	err = env.Exit()
	if err != nil {
		t.Fatal(err)
	}

}