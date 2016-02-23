package testuser

import (
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

var (
	userName = upspin.UserName("joe@blow.com")
)

func setup(t *testing.T) (upspin.User, *upspin.Context) {
	c := &upspin.Context{
		Packing: upspin.DebugPack,
	}
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	u, err := access.BindUser(c, e)
	if err != nil {
		t.Fatal(err)
	}
	c.Store, err = access.BindStore(c, e)
	if err != nil {
		t.Fatal(err)
	}
	c.Directory, err = access.BindDirectory(c, e)
	if err != nil {
		t.Fatal(err)
	}

	return u, c
}

func TestInstallAndLookup(t *testing.T) {
	u, ctxt := setup(t)
	testUser, ok := u.(*Service)
	if !ok {
		t.Fatal("Not a testuser Service")
	}

	err := testUser.Install(userName, ctxt.Directory)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	eRecv, keys, err := u.Lookup(userName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Expected no keys for user %v, got %d", userName, len(keys))
	}
	if len(eRecv) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(eRecv))
	}
	if eRecv[0].Transport != upspin.InProcess {
		t.Errorf("Expected endpoint to be %d, but instead it was %d", upspin.InProcess, eRecv[0].Transport)
	}
}

func TestPublicKeysAndUsers(t *testing.T) {
	u, _ := setup(t)
	testUser, ok := u.(*Service)
	if !ok {
		t.Fatal("Not a testuser Service")
	}
	const testKey = "pub key1"
	testUser.SetPublicKeys(userName, []upspin.PublicKey{
		upspin.PublicKey(testKey),
	})

	_, keys, err := u.Lookup(userName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key for user %v, got %d", userName, len(keys))
	}
	if string(keys[0]) != testKey {
		t.Errorf("Expected key %s, got %s", testKey, keys[0])
	}

	users := testUser.ListUsers()
	if len(users) != 1 {
		t.Fatalf("Expected 1 user, got %d", len(users))
	}
	if users[0] != userName {
		t.Errorf("Expected user %s, got %v", userName, users[0])
	}

	// Delete keys for user
	testUser.SetPublicKeys(userName, nil)

	users = testUser.ListUsers()
	if len(users) != 0 {
		t.Fatalf("Expected 0 users, got %d", len(users))
	}
}
