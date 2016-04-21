package jsonmsg

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverParsingErrorPrefix = "can't parse reply from server:"
)

var (
	location = upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   "some server",
		},
		Reference: "abcd",
	}
	dirEntry = upspin.DirEntry{
		Name:     "foo",
		Location: location,
	}
)

func TestLocationResponse(t *testing.T) {
	locJSON, err := json.Marshal(location)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := LocationResponse(locJSON)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if loc == nil {
		t.Fatal("Expected a valid location, got nil")
	}
	if loc.Reference != location.Reference {
		t.Errorf("Expected key %v, got %v", location.Reference, loc.Reference)
	}
}

func TestLocationResponseBadError(t *testing.T) {
	loc, err := LocationResponse([]byte(`{"endpoint:"foo", "bla bla bla"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestLocationResponseEmptyError(t *testing.T) {
	loc, err := LocationResponse([]byte(""))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestLocationResponseWithProperError(t *testing.T) {
	loc, err := LocationResponse([]byte(`{"error":"something bad happened"}`))
	if err == nil {
		t.Fatalf("Expected error, got nil: %v", loc)
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	expectedError := "something bad happened"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestRefResponse(t *testing.T) {
	ref, err := ReferenceResponse([]byte(`{"ref": "1234"}`))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if ref != "1234" {
		t.Errorf("Expected key 1234, got %v", ref)
	}
}

func TestRefResponseBadError(t *testing.T) {
	ref, err := ReferenceResponse([]byte("bla bla bla"))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if ref != "" {
		t.Fatalf("Expected a nil key, got %v", ref)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponse(t *testing.T) {
	dirJSON, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := DirEntryResponse(dirJSON)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if dir == nil {
		t.Fatal("Expected a valid dirEntry, got nil")
	}
	if dir.Location.Reference != dirEntry.Location.Reference {
		t.Errorf("Expected key %v, got %v", dirEntry.Location.Reference, dir.Location.Reference)
	}
}

func TestDirEntryResponseBadError(t *testing.T) {
	dir, err := DirEntryResponse([]byte(`{"Name":"path","Location":"loc"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if dir != nil {
		t.Fatalf("Expected a nil DirEntry, got %v", *dir)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponseNilDirEntry(t *testing.T) {
	dir, err := DirEntryResponse(nil)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestDirEntryResponseEmptyDirEntry(t *testing.T) {
	dir, err := DirEntryResponse([]byte(""))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestDirEntryResponseInvalidDirEntry(t *testing.T) {
	dir, err := DirEntryResponse([]byte("bla"))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponseWithProperError(t *testing.T) {
	dir, err := DirEntryResponse([]byte(`{"error":"something terrible happened"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if dir != nil {
		t.Fatalf("Expected a nil DirEntry, got %v", *dir)
	}
	expectedError := "something terrible happened"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestWhichAccessResponse(t *testing.T) {
	acc, err := WhichAccessResponse([]byte(`{"error": "not found"}`))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if err.Error() != "not found" {
		t.Fatalf("Expected 'not found', got %s", err)
	}
	// Now try a valid one
	const path = "foo@bar.com/Access"
	acc, err = WhichAccessResponse([]byte(fmt.Sprintf(`{"Access":"%s"}`, path)))
	if err != nil {
		t.Fatal(err)
	}
	if acc != upspin.PathName(path) {
		t.Fatalf("Expected path %s, got %s", path, acc)
	}
}

func TestUserLookupResponse(t *testing.T) {
	user, endpoints, keys, err := UserLookupResponse([]byte(`{"error":"unknown user"}`))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	expectedError := "unknown user"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %s, got %s", expectedError, err)
	}
	// Now a valid response
	const (
		userName = "bob@foo.com"
		pubKey1  = "pub key 1"
		pubKey2  = "pub key 2"
	)
	ue := userEntry{
		User: userName,
		Endpoints: []upspin.Endpoint{upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   upspin.NetAddr("http://hello.com"),
		}},
		Keys: []upspin.PublicKey{pubKey1, pubKey2},
	}
	ueJSON, err := json.Marshal(ue)
	if err != nil {
		t.Fatal(err)
	}
	user, endpoints, keys, err = UserLookupResponse(ueJSON)
	if err != nil {
		t.Fatal(err)
	}
	if user != userName {
		t.Fatalf("Expected user %s, got %s", userName, user)
	}
	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoint.String(&endpoints[0]) != endpoint.String(&ue.Endpoints[0]) {
		t.Fatalf("Expected endpoint %v, got %v", ue.Endpoints[0], endpoints[0])
	}
	if len(keys) != 2 {
		t.Fatalf("Expected 2 keys, got %d", len(keys))
	}
	if keys[0] != pubKey1 {
		t.Fatalf("Expected key %s, got %s", pubKey1, keys[0])
	}
	if keys[1] != pubKey2 {
		t.Fatalf("Expected key %s, got %s", pubKey2, keys[0])
	}
}
