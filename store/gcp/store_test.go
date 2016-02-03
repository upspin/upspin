package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	errSomethingBad = "Something bad happened on the internet"
	errBrokenPipe   = "The internet has a broken pipe"
)

var (
	newReference = upspin.Reference{
		Key:     "newKey",
		Packing: upspin.PlainPack,
	}
	newEndpoint = upspin.Endpoint{
		Transport: upspin.HTTP,
	}
	newLocation = upspin.Location{
		Reference: newReference,
		Endpoint:  newEndpoint,
	}
)

func TestStorePutError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errSomethingBad),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})

	s := New("http://localhost:8080", mock)
	ref := upspin.Reference{
		Key:     "1234",
		Packing: upspin.PlainPack,
	}

	_, err := s.Put(ref, []byte("contents"))

	expected := fmt.Sprintf("Error putting data to server: %s", errSomethingBad)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
}

func TestStorePut(t *testing.T) {
	// The server will respond with a location for the object.
	mock := nettest.NewMockHTTPClient(createMockResponse(t))

	s := New("http://localhost:8080", mock)
	ref := upspin.Reference{
		Key:     "1234",
		Packing: upspin.PlainPack,
	}

	loc, err := s.Put(ref, []byte("contents"))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if loc != newLocation {
		t.Fatalf("Server gave us wrong location. Expected %v, got %v", newLocation, loc)
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

	s := New("http://localhost:8080", mock)
	ref := upspin.Reference{
		Key:     "1234",
		Packing: upspin.PlainPack,
	}
	loc := upspin.Location{
		Reference: ref,
	}

	_, _, err := s.Get(loc)

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
	s := New("http://localhost:8080", mock)
	ref := upspin.Reference{
		Key:     "",
		Packing: upspin.PlainPack,
	}
	loc := upspin.Location{
		Reference: ref,
	}

	_, _, err := s.Get(loc)

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
	mock := nettest.NewMockHTTPClient(createMockResponse(t))

	s := New("http://localhost:8080", mock)

	ref := upspin.Reference{
		Key:     "XX some hash XX",
		Packing: upspin.EEp256Pack,
	}
	loc := upspin.Location{
		Reference: ref,
		Endpoint: upspin.Endpoint{
			Transport: upspin.GCP,
		},
	}

	data, locs, err := s.Get(loc)

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
	expectedQuery := "ref=XX some hash XX"
	if u.RawQuery != expectedQuery {
		t.Fatalf("Wrong query params: expected %v, got %v", expectedQuery, u.RawQuery)
	}
}

func createMockResponse(t *testing.T) []nettest.MockHTTPResponse {
	newLoc, err := json.Marshal(newLocation)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", newLoc)
	return []nettest.MockHTTPResponse{resp}
}
