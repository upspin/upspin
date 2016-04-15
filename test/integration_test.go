// Package test contains an integration test for all of Upspin.
// This particular integration test runs on GCP and as such we disable it
// from normal 'go test ./...' runs since it's too
// expensive. To run it, do 'go test -tags integration'
// TODO: move all or most of client/integration_test here.

// +build integration

package test

import (
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	e "upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	ownersName       = "upspin-test@google.com"
	readerName       = "upspin-friend-test@google.com"
	unauthorizedUser = "sally@unauthorized.com"
	contentsOfFile1  = "contents of file 1"
	contentsOfFile2  = "contents of file 2..."
	contentsOfFile3  = "===PDF PDF PDF=="

	hasLocation = true
)

var (
	setup = e.Setup{
		Tree: e.Tree{
			e.E("/dir1/", ""),
			e.E("/dir2/", ""),
			e.E("/dir1/file1.txt", contentsOfFile1),
			e.E("/dir2/file2.txt", contentsOfFile2),
			e.E("/dir2/file3.pdf", contentsOfFile3),
		},
		OwnerName:                 ownersName,
		Keys:                      ownersKey,
		Transport:                 upspin.GCP,
		IgnoreExistingDirectories: false, // left-over Access files would be a problem.
		DeleteTreeAtExit:          true,
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
	readerClient upspin.Client
)

func testNoReadersAllowed(t *testing.T, env *e.Env) {
	var err error
	readerClient, err = env.NewUser(readerName, &readersKey)
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(ownersName + "/dir1/file1.txt")
	_, err = readerClient.Get(fileName)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error contains %s, got %s", access.ErrPermissionDenied, err)
	}
	// But the owner can still read it.
	data, err := env.Client.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, data)
	}
}

func testAllowListAccess(t *testing.T, env *e.Env) {
	_, err := env.Client.Put(ownersName+"/dir1/Access", []byte("l:"+readerName))
	if err != nil {
		t.Fatal(err)
	}
	// Now check that readerClient can list file1, but can't read and therefore the Location is zeroed out.
	file := ownersName + "/dir1/file1.txt"
	dirs, err := readerClient.Glob(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), !hasLocation, len(contentsOfFile1))

	// Ensure we can't read the data.
	_, err = readerClient.Get(upspin.PathName(file))
	if err == nil {
		t.Errorf("Expected error, got none")
	}
}

func testAllowReadAccess(t *testing.T, env *e.Env) {
	_, err := env.Client.Put(ownersName+"/dir1/Access", []byte("r:"+readerName))
	if err != nil {
		t.Fatal(err)
	}
	data, err := readerClient.Get(upspin.PathName(ownersName + "/dir1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, data)
	}
}

func testGlobWithLimitedAccess(t *testing.T, env *e.Env) {
	// Owner sees both files.
	pattern := ownersName + "/dir*/*.txt"
	dirs, err := env.Client.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("Expected 2 dirs, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
	checkDirEntry(t, dirs[1], upspin.PathName(ownersName+"/dir2/file2.txt"), hasLocation, len(contentsOfFile2))

	// readerClient should only be able to see contents of dir1 not dir2.
	dirs, err = readerClient.Glob(pattern)
	if len(dirs) != 1 {
		t.Fatalf("Expected 2 dirs, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
}

func TestAll(t *testing.T) {
	env, err := e.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	// The ordering here is important as each test adds state to the tree.
	testNoReadersAllowed(t, env)
	testAllowListAccess(t, env)
	testAllowReadAccess(t, env)
	testGlobWithLimitedAccess(t, env)

	err = env.Exit()
	if err != nil {
		t.Fatal(err)
	}
}

// checkDirEntry verifies a dir entry against expectations. size == 0 for don't check.
func checkDirEntry(t *testing.T, dirEntry *upspin.DirEntry, name upspin.PathName, hasLocation bool, size int) {
	if dirEntry.Name != name {
		t.Errorf("Expected name %s, got %s", name, dirEntry.Name)
	}
	var zeroLoc upspin.Location
	if dirEntry.Location == zeroLoc {
		if hasLocation {
			t.Errorf("Expected %s to have location", name)
		}
	} else {
		if !hasLocation {
			t.Errorf("Expected %s not to have location, got %v", name, dirEntry.Location)
		}
	}
	if size != 0 && dirEntry.Metadata.Size != uint64(size) {
		t.Errorf("Expected %s has size %d, got %d", name, size, dirEntry.Metadata.Size)
	}
}
