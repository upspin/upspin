package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	errSomethingBad = "Something bad happened on the internet"
	errBrokenPipe   = "The internet has a broken pipe"
	contentKey      = "my key"
)

var (
	keyStruct = struct{ Key string }{Key: contentKey}

	newLocation = upspin.Location{
		Reference: upspin.Reference{
			Key: "new key",
		},
	}
)

func TestStorePutError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errSomethingBad),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})
	s := newStore("http://localhost:8080", mock)

	_, err := s.Put([]byte("contents"))

	expected := fmt.Sprintf("Error putting data to server: %s", errSomethingBad)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
}

func TestStorePut(t *testing.T) {
	// The server will respond with a location for the object.
	mock := nettest.NewMockHTTPClient(createMockPutResponse(t))
	s := newStore("http://localhost:8080", mock)

	key, err := s.Put([]byte("contents"))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if key != contentKey {
		t.Fatalf("Server gave us wrong location. Expected %v, got %v", contentKey, key)
	}
	// Verifies the server received the proper request
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Sent more requests than necessary. Expected 1, got %v", len(reqs))
	}
	u := reqs[0].URL
	if u.Scheme != "http" {
		t.Fatalf("Expected http request, got %v", u.Scheme)
	}
	if u.Host != "localhost:8080" {
		t.Fatalf("Expected request to localhost:8080, got %v", u.Host)
	}
	if u.Path != "/put" {
		t.Fatalf("Expected request to /put, got %v", u.Path)
	}
	if u.RawQuery != "" {
		t.Fatalf("Expected no query params, got %v", u.RawQuery)
	}
}

func TestStoreGetError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errBrokenPipe),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})
	s := newStore("http://localhost:8080", mock)

	_, _, err := s.Get("1234")

	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}
	expected := fmt.Sprintf("Error getting data from server: %s", errBrokenPipe)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
}

func TestStoreGetErrorEmptyKey(t *testing.T) {
	// Our request is invalid.
	mock := nettest.NewMockHTTPClient(nil)
	s := newStore("http://localhost:8080", mock)

	_, _, err := s.Get("")

	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}
	expected := "Key can't be empty"
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
}

func TestStoreGetRedirect(t *testing.T) {
	// The server will redirect the client to a new location
	mock := nettest.NewMockHTTPClient(createMockGetResponse(t))

	s := newStore("http://localhost:8080", mock)

	const LookupKey = "XX some hash XX"
	data, locs, err := s.Get(LookupKey)

	if data != nil {
		t.Fatal("Got data when we expected to get redirected")
	}
	if err != nil {
		t.Fatalf("Got an unexpected error: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("Expected 1 location, got %d", len(locs))
	}
	if locs[0] != newLocation {
		t.Fatalf("Server gave us wrong location. Expected %v, got %v", newLocation, locs[0])
	}
	// Verifies request was sent correctly
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Sent more requests than necessary. Expected 1, got %v", len(reqs))
	}
	u := reqs[0].URL
	if u.Scheme != "http" {
		t.Fatalf("Expected http request, got %v", u.Scheme)
	}
	if u.Host != "localhost:8080" {
		t.Fatalf("Expected request to localhost:8080, got %v", u.Host)
	}
	if u.Path != "/get" {
		t.Fatalf("Expected request to /get, got %v", u.Path)
	}
	expectedQuery := fmt.Sprintf("ref=%s", LookupKey)
	if u.RawQuery != expectedQuery {
		t.Fatalf("Wrong query params: expected %v, got %v", expectedQuery, u.RawQuery)
	}
}

func createMockGetResponse(t *testing.T) []nettest.MockHTTPResponse {
	newLocJSON, err := json.Marshal(newLocation)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", newLocJSON)
	return []nettest.MockHTTPResponse{resp}
}

func createMockPutResponse(t *testing.T) []nettest.MockHTTPResponse {
	keyStructJSON, err := json.Marshal(keyStruct)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", keyStructJSON)
	return []nettest.MockHTTPResponse{resp}
}

func newStore(serverURL string, client HTTPClientInterface) upspin.Store {
	context := Context{
		Client: client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(serverURL),
	}
	s, err := access.BindStore(context, e)
	if err != nil {
		log.Fatalf("Can't bind: %v", err)
	}
	return s
}
