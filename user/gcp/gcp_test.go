// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"strings"
	"testing"

	"upspin.io/cloud/storage/storagetest"
	"upspin.io/upspin"
)

const (
	mockUser = "bob@foo.com"
)

func TestInvalidUser(t *testing.T) {
	u := newDummyUserServer()
	_, _, err := u.Lookup("a")
	if err == nil {
		t.Fatal("Expected an error")
	}
	expected := "invalid user"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Expected %q, got %q", expected, err)
	}
}

func TestAddKeyShortKey(t *testing.T) {
	u := newDummyUserServer()
	err := u.AddKey("a@abc.com", upspin.PublicKey("1234"))
	if err == nil {
		t.Fatal("Expected an error")
	}
	expected := "key length too short"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Expected %q, got %q", expected, err)
	}
}

func TestAddKeyToExistingUser(t *testing.T) {
	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Keys":["xyz"]}`))
	err := u.AddKey(mockUser, upspin.PublicKey("abcdefghijklmnopqrs"))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != mockUser {
		t.Errorf("Expected update to user %s, got user %s", mockUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"bob@foo.com","Keys":["abcdefghijklmnopqrs","xyz"],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddExistingKey(t *testing.T) {
	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Keys":["abcdefghijklmnopqrs"]}`))
	err := u.AddKey(mockUser, upspin.PublicKey("abcdefghijklmnopqrs"))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP did not try to Put the repeated key back.
	if len(mockGCP.PutRef) != 0 || len(mockGCP.PutContents) != 0 {
		t.Fatalf("Expected no call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
}

func TestAddKeyToNewUser(t *testing.T) {
	u, mockGCP := newUserServerWithMocking(nil)
	err := u.AddKey("new@user.com", upspin.PublicKey("abcdefghijklmnopqrs"))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	newUser := "new@user.com"
	if mockGCP.PutRef[0] != newUser {
		t.Errorf("Expected update to user %s, got user %s", newUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"new@user.com","Keys":["abcdefghijklmnopqrs"],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddRootToExistingUser(t *testing.T) {
	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Endpoints":[{"Transport":2,"NetAddr":"http://here.com"}]}`))
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr("http://there.co.uk"),
	}
	err := u.AddRoot(mockUser, e)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != mockUser {
		t.Errorf("Expected update to user %s, got user %s", mockUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"bob@foo.com","Keys":null,"Endpoints":[{"Transport":1,"NetAddr":"http://there.co.uk"},{"Transport":2,"NetAddr":"http://here.com"}]}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddRootToNewUser(t *testing.T) {
	u, mockGCP := newUserServerWithMocking(nil)
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr("http://there.co.uk"),
	}
	err := u.AddRoot("new@user.com", e)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	newUser := "new@user.com"
	if mockGCP.PutRef[0] != newUser {
		t.Errorf("Expected update to user %s, got user %s", newUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"new@user.com","Keys":null,"Endpoints":[{"Transport":1,"NetAddr":"http://there.co.uk"}]}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestGetExistingUser(t *testing.T) {
	const storedEntry = `{"User":"bob@foo.com","Keys":["my key"],"Endpoints":[{"Transport":2,"NetAddr":"http://here.com"}]}`
	u, _ := newUserServerWithMocking([]byte(storedEntry))
	e, keys, err := u.Lookup(mockUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key, got %d", len(keys))
	}
	expectedKey := "my key"
	if string(keys[0]) != expectedKey {
		t.Errorf("Expected key %q, got %q", expectedKey, keys[0])
	}
	if len(e) != 1 {
		t.Fatalf("Expected one endpoint, got %d", len(e))
	}
	expectedEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("http://here.com"),
	}
	if e[0] != expectedEndpoint {
		t.Errorf("Expected endpoint %v, got %v", expectedEndpoint, e[0])
	}
}

func newDummyUserServer() *user {
	return &user{cloudClient: &storagetest.DummyStorage{}}
}

// newUserServerWithMocking sets up a mock GCP client that expects a
// single lookup of user mockUser and it will reply with the preset
// data. It returns the user server, the mock GCP client for further
// verification.
func newUserServerWithMocking(data []byte) (*user, *storagetest.ExpectDownloadCapturePut) {
	mockGCP := &storagetest.ExpectDownloadCapturePut{
		Ref:         []string{mockUser},
		Data:        [][]byte{data},
		PutContents: make([][]byte, 0, 1),
		PutRef:      make([]string, 0, 1),
	}
	u := &user{cloudClient: mockGCP}
	return u, mockGCP
}
