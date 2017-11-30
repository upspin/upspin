// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"encoding/json"
	"reflect"
	"testing"

	"upspin.io/cache"
	"upspin.io/cloud/storage/storagetest"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

const isAdmin = true

func TestLookupInvalidUser(t *testing.T) {
	userName := upspin.UserName("a")

	u := newDummyKeyServer()
	_, err := u.Lookup(userName)
	expectedErr := errors.E(errors.Invalid, userName)
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want = %s", err, expectedErr)
	}
}

func TestLookup(t *testing.T) {
	const (
		myName    = "user@example.com"
		otherUser = "other@domain.org"
	)

	user := &upspin.User{
		Name: otherUser,
		Dirs: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("there.co.uk"),
			},
		},
		Stores: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("down-under.au"),
			},
		},
		PublicKey: upspin.PublicKey("my key"),
	}
	buf := marshalUser(t, user, !isAdmin)

	// Create a server authenticated with myName and with a pre-existing User entry for myName.
	u, _ := newKeyServerWithMocking(myName, otherUser, buf)

	retUser, err := u.Lookup(otherUser)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*retUser, *user) {
		t.Errorf("returned = %v, want = %v", retUser, user)
	}
}

func BenchmarkLookup(b *testing.B) {
	b.StopTimer()
	k := benchKeyServer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		_, err := k.Lookup("other@domain.org")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// benchKeyServer creates a server bound to user@example.com that can lookup an
// existing user named "other@domain.org",
func benchKeyServer() *server {
	const (
		myName    = "user@example.com"
		otherUser = "other@domain.org"
	)

	user := &upspin.User{
		Name: otherUser,
		Dirs: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("there.co.uk"),
			},
		},
		Stores: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("down-under.au"),
			},
		},
		PublicKey: upspin.PublicKey("my key"),
	}
	ue := userEntry{
		User:    *user,
		IsAdmin: isAdmin,
	}
	buf, err := json.Marshal(ue)
	if err != nil {
		panic(err)
	}

	// Create a server authenticated with myName and with a pre-existing User entry for myName.
	u, _ := newKeyServerWithMocking(myName, otherUser, buf)

	return u
}

func TestNotAdminPutOther(t *testing.T) {
	const (
		myName    = "cool@dude.com"
		otherUser = "uncool@buddy.com"
	)

	// Pre-existing user: myName, who *is not* an admin.
	user := &upspin.User{
		Name: myName,
	}
	buf := marshalUser(t, user, !isAdmin)

	// Create a server authenticated with myName and with a pre-existing User entry for myName.
	u, mockGCP := newKeyServerWithMocking(myName, myName, buf)

	// myName now attempts to write somebody else's information.
	otherU := &upspin.User{
		Name:      otherUser,
		PublicKey: upspin.PublicKey("going to change your key, haha"),
	}
	err := u.Put(otherU)
	expectedErr := errors.E(errors.Permission, upspin.UserName(myName), "not an administrator for buddy.com")
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want = %s", err, expectedErr)
	}
	// Check that indeed we did not write to GCP.
	if len(mockGCP.PutRef) != 0 {
		t.Errorf("Expected no writes, got %d", len(mockGCP.PutRef))
	}
}

func TestIsAdminPutOther(t *testing.T) {
	const (
		myName    = "cool@dude.com"
		otherUser = "uncool@buddy.com"
	)

	// Pre-existing user: myName, who *is* an admin.
	user := &upspin.User{
		Name: myName,
	}
	buf := marshalUser(t, user, isAdmin)

	// Create a server authenticated with myName and with a pre-existing User entry for myName.
	u, mockGCP := newKeyServerWithMocking(myName, myName, buf)

	// myName now attempts to write somebody else's information.
	otherU := &upspin.User{
		Name:      otherUser,
		PublicKey: upspin.PublicKey("going to change your key, because I can"),
	}
	err := u.Put(otherU)
	if err != nil {
		t.Fatal(err)
	}
	// Check new user was written to GCP
	if len(mockGCP.PutRef) != 1 {
		t.Fatalf("Expected one write, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != otherUser {
		t.Errorf("put = %s, want = %s", mockGCP.PutRef[0], otherUser)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *otherU) {
		t.Errorf("saved = %v, want = %v", savedUser, otherU)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
	}
}

func TestPutSelf(t *testing.T) {
	const myName = "cool@dude.com"

	// New server for myName.
	u, mockGCP := newKeyServerWithMocking(myName, "", nil)

	user := &upspin.User{
		Name: myName,
		Dirs: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("there.co.uk"),
			},
		},
		Stores: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("down-under.au"),
			},
		},
		PublicKey: upspin.PublicKey("my key"),
	}
	err := u.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP received the Put.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("num calls = %d, want = 1", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != myName {
		t.Errorf("put = %s, want = %s", mockGCP.PutRef[0], myName)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *user) {
		t.Errorf("saved = %v, want = %v", savedUser, user)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
	}
}

func TestIsAdminPutExistingSelf(t *testing.T) {
	const myName = "cool@dude.com"

	user := &upspin.User{
		Name: myName,
		Stores: []upspin.Endpoint{
			{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("some.place:443"),
			},
		},
		PublicKey: upspin.PublicKey("super secure"),
	}
	buf := marshalUser(t, user, isAdmin)

	// New server for myName.
	u, mockGCP := newKeyServerWithMocking(myName, myName, buf)

	// Changing my user info to include a root dir.
	user.Dirs = append(user.Dirs, upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("my-root-dir:443"),
	})
	// Change my information.
	err := u.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP received the Put.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("num calls = %d, want = 1", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != myName {
		t.Errorf("put = %s, want = %s", mockGCP.PutRef[0], myName)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *user) {
		t.Errorf("saved = %v, want = %v", savedUser, user)
	}
	if !isAdmin {
		t.Error("Expected user to be an admin")
	}
}

func TestNotAdminPutSuffixedSelf(t *testing.T) {
	const (
		myName    = "cool@dude.com"
		otherUser = "cool+suffix@dude.com"
	)

	// Pre-existing user: myName, who is not an admin.
	user := &upspin.User{
		Name: myName,
	}
	buf := marshalUser(t, user, !isAdmin)

	// Create a server authenticated with myName and with a pre-existing User entry for myName.
	u, mockGCP := newKeyServerWithMocking(myName, myName, buf)

	// myName now attempts to write somebody else's information.
	otherU := &upspin.User{
		Name:      otherUser,
		PublicKey: upspin.PublicKey("new key"),
	}
	err := u.Put(otherU)
	if err != nil {
		t.Fatal(err)
	}
	// Check new user was written to GCP
	if len(mockGCP.PutRef) != 1 {
		t.Fatalf("num calls = %d, want = 1", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != otherUser {
		t.Errorf("put = %s, want = %s", mockGCP.PutRef[0], otherUser)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *otherU) {
		t.Errorf("saved = %v, want = %v", savedUser, user)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
	}
}

func TestPutWildcardUser(t *testing.T) {
	const myName = "*@mydomain.com"
	user := &upspin.User{
		Name: myName,
	}
	buf := marshalUser(t, user, isAdmin)

	// New server for myName.
	u, _ := newKeyServerWithMocking(myName, myName, buf)

	// Change my information.
	err := u.Put(user)
	expectedErr := errors.E(errors.Invalid, upspin.UserName(myName))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}
}

func TestIsDomainAdminPutOther(t *testing.T) {
	const (
		// domainAdmin is an admin for otherDude's domain.
		domainAdmin = "bob@master.com"
		otherDude   = "other@dude.com"
	)

	// Get the test key for bob.
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "bob"))
	if err != nil {
		t.Fatal(err)
	}

	adminUser := &upspin.User{
		Name:      domainAdmin,
		PublicKey: f.PublicKey(),
	}
	adminJSON := marshalUser(t, adminUser, !isAdmin)

	// New server for domainAdmin.
	u, mockGCP := newKeyServerWithMocking(domainAdmin, domainAdmin, adminJSON)

	// Setup fake DNS domain with domain admin's signature.
	lookupTXT := func(domain string) ([]string, error) {
		if domain == "dude.com" {
			return []string{
				"some unrelated TXT field",
				"upspin:aaabbbbbbb1234-bbccfffeeeddd0003344347273", // someone else's signature.
				"upspin:4f1f4d29537fe0239f21d1384c32c61795360c744ad4f6f474f46dd7c2d03edb-494d8cb1988121ee0056fb49d182ab200dd5ad3572f28a47444ed41e8e947123",
			}, nil
		}
		return nil, errors.Str("no host found")
	}
	u.lookupTXT = lookupTXT

	// adminUser will now Put a new user record for otherDude.
	user := &upspin.User{
		Name:      otherDude,
		PublicKey: upspin.PublicKey("adminUser can Put this"),
	}
	err = u.Put(user)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP received the Put.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("num calls = %d, want = 1", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != otherDude {
		t.Errorf("put = %s, want = %s", mockGCP.PutRef[0], otherDude)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *user) {
		t.Errorf("saved = %v, want = %v", savedUser, adminUser)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
	}

	// Now try to update otherDude's record and fail. Even domain admins
	// cannot do that.

	// Get a new server.
	otherDudeJSON := mockGCP.PutContents[0]
	u, mockGCP = newKeyServerWithMocking(domainAdmin, domainAdmin, adminJSON)
	mockGCP.Data = append(mockGCP.Data, otherDudeJSON)
	mockGCP.Ref = append(mockGCP.Ref, otherDude)

	user = &upspin.User{
		Name:      otherDude,
		PublicKey: upspin.PublicKey("adminUser cannot update a user!"),
	}
	err = u.Put(user)
	expectedErr := errors.E(errors.Permission, upspin.UserName(domainAdmin))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %s, want = %s", err, expectedErr)
	}

	// Try to add users with old style signatures, one where the message is
	// not hashed. It should fail.
	lookupTXT = func(domain string) ([]string, error) {
		if domain == "dude.com" {
			return []string{
				"some unrelated TXT field",
				"upspin:39b3c02492b39fcb8f22a4255235de6e1656f471738f7b8f61445b4938fba658-bae1618c8c42ced79f0a14b5208dfc8ecd4ad103d805117adebe595f447872a9",
			}, nil
		}
		return nil, errors.Str("no host found")
	}
	// New server for domainAdmin.
	u, _ = newKeyServerWithMocking(domainAdmin, domainAdmin, adminJSON)
	u.lookupTXT = lookupTXT
	// adminUser will now Put a new user record for otherDude.
	user = &upspin.User{
		Name:      otherDude,
		PublicKey: upspin.PublicKey("adminUser can Put this"),
	}
	err = u.Put(user)
	if !errors.Is(errors.Permission, err) {
		t.Fatalf("Expected Permission Denied, got %s", err)
	}
}

// marshalUser marshals the user struct and whether the user is an admin into JSON bytes.
func marshalUser(t *testing.T, user *upspin.User, isAdmin bool) []byte {
	ue := userEntry{
		User:    *user,
		IsAdmin: isAdmin,
	}
	buf, err := json.Marshal(ue)
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

// unmarshalUser unmarshals JSON bytes into the user struct, along with whether the user is an admin.
func unmarshalUser(t *testing.T, buf []byte) (*upspin.User, bool) {
	var ue userEntry
	err := json.Unmarshal(buf, &ue)
	if err != nil {
		t.Fatalf("Wrote invalid bytes: %q: %v", buf, err)
	}
	return &ue.User, ue.IsAdmin
}

// newDummyKeyServer creates a new keyserver.
func newDummyKeyServer() *server {
	dummy, _ := storagetest.DummyStorage(nil)
	return &server{storage: dummy}
}

// newKeyServerWithMocking sets up a mock GCP client for a user and expects a
// single lookup of user mockKey and it will reply with the preset
// data. It returns the user server, the mock GCP client for further
// verification.
func newKeyServerWithMocking(user upspin.UserName, ref string, data []byte) (*server, *storagetest.ExpectDownloadCapturePut) {
	mockGCP := &storagetest.ExpectDownloadCapturePut{
		Ref:         []string{ref},
		Data:        [][]byte{data},
		PutContents: make([][]byte, 0, 1),
		PutRef:      make([]string, 0, 1),
	}
	s := &server{
		storage:   mockGCP,
		user:      user,
		lookupTXT: mockLookupTXT,
		logger:    &noopLogger{},
		cache:     cache.NewLRU(10),
		negCache:  cache.NewLRU(10),
	}
	return s, mockGCP
}

func mockLookupTXT(domain string) ([]string, error) {
	return nil, nil
}
