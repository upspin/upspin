// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"strings"
	"testing"

	"reflect"

	"gopkg.in/square/go-jose.v1/json"
	"upspin.io/cloud/storage/storagetest"
	"upspin.io/context"
	"upspin.io/upspin"
)

const isAdmin = true

func TestInvalidUser(t *testing.T) {
	u := newDummyKeyServer()
	_, err := u.Lookup("a")
	if err == nil {
		t.Fatal("Expected an error")
	}
	expected := "invalid user"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Expected %q, got %q", expected, err)
	}
}

func TestPutOtherUserNotAdmin(t *testing.T) {
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
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("Expected permission denied, got %q", err)
	}
	// Check that indeed we did not write to GCP.
	if len(mockGCP.PutRef) != 0 {
		t.Errorf("Expected no writes, got %d", len(mockGCP.PutRef))
	}
}

func TestPutOtherUserIsAdmin(t *testing.T) {
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
		t.Fatal("Expected no error")
	}
	// Check new user was written to GCP
	if len(mockGCP.PutRef) != 1 {
		t.Fatalf("Expected one write, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != otherUser {
		t.Errorf("Expected write to user %q, wrote to %q", otherUser, mockGCP.PutRef[0])
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *otherU) {
		t.Errorf("Expected Put to store User %v, but it stored %v", otherU, savedUser)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
	}
}

func TestPutNewUserSelf(t *testing.T) {
	const myName = "cool@dude.com"

	// New server for myName.
	u, mockGCP := newKeyServerWithMocking(myName, "", nil)

	user := &upspin.User{
		Name: myName,
		Dirs: []upspin.Endpoint{
			upspin.Endpoint{
				Transport: upspin.Remote,
				NetAddr:   upspin.NetAddr("there.co.uk"),
			},
		},
		Stores: []upspin.Endpoint{
			upspin.Endpoint{
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
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != myName {
		t.Errorf("Expected write to %q, wrote to %q", myName, mockGCP.PutRef)
	}
	savedUser, isAdmin := unmarshalUser(t, mockGCP.PutContents[0])
	if !reflect.DeepEqual(*savedUser, *user) {
		t.Errorf("Expected Put to store User %v, but it stored %v", user, savedUser)
	}
	if isAdmin {
		t.Error("Expected user not to be an admin")
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
func newDummyKeyServer() *key {
	return &key{cloudClient: &storagetest.DummyStorage{}}
}

// newKeyServerWithMocking sets up a mock GCP client for a user and expects a
// single lookup of user mockKey and it will reply with the preset
// data. It returns the user server, the mock GCP client for further
// verification.
func newKeyServerWithMocking(user upspin.UserName, ref string, data []byte) (*key, *storagetest.ExpectDownloadCapturePut) {
	mockGCP := &storagetest.ExpectDownloadCapturePut{
		Ref:         []string{ref},
		Data:        [][]byte{data},
		PutContents: make([][]byte, 0, 1),
		PutRef:      make([]string, 0, 1),
	}
	u := &key{
		cloudClient: mockGCP,
		context:     context.New().SetUserName(user),
	}
	return u, mockGCP
}
