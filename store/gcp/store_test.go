package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	newReference = upspin.Reference{
		Key:      "newKey",
		Protocol: upspin.HTTP,
	}
	newLocation = upspin.Location{
		Reference: newReference,
	}
)

func TestStorePutError(t *testing.T) {
	mock := &nettest.MockHttpClient{}
	mock.SetResponse(nil, errors.New("Something bad happened on the internet"))
	s := New("http://localhost", 8080, mock)
	ref := upspin.Reference{
		Key:      "1234",
		Protocol: upspin.HTTP,
	}

	_, err := s.Put(ref, []byte("contents"))

	if err.Error() != "Error putting data to server: Something bad happened on the internet" {
		t.Fatalf("Test failed: %v", err)
	}
}

func TestStorePut(t *testing.T) {
	// The "server" will respond with a location for the object
	resp := createMockResponse(t)
	mock := &nettest.MockHttpClient{}
	mock.SetResponse(resp, nil)

	s := New("http://localhost", 8080, mock)
	ref := upspin.Reference{
		Key:      "1234",
		Protocol: upspin.HTTP,
	}

	loc, err := s.Put(ref, []byte("contents"))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if loc != newLocation {
		t.Fatalf("Server gave us wrong location. Expected: %v, got: %v", newLocation, loc)
	}
}

func TestStoreGetError(t *testing.T) {
	mock := &nettest.MockHttpClient{}
	mock.SetResponse(nil, errors.New("Net has broken pipes"))
	s := New("http://localhost", 8080, mock)
	ref := upspin.Reference{
		Key:      "1234",
		Protocol: upspin.HTTP,
	}
	loc := upspin.Location{
		Reference: ref,
	}

	_, _, err := s.Get(loc)

	if err.Error() != "Error getting data from server: Net has broken pipes" {
		t.Fatalf("Test failed: %v", err)
	}
}

func TestStoreGetErrorWrongProtocol(t *testing.T) {
	mock := &nettest.MockHttpClient{}
	s := New("http://localhost", 8080, mock)
	ref := upspin.Reference{
		Key:      "1234",
		Protocol: upspin.Protocol(99),
	}
	loc := upspin.Location{
		Reference: ref,
	}

	_, _, err := s.Get(loc)

	if err.Error() != "Can't figure out the protocol" {
		t.Fatalf("Test failed: %v", err)
	}
}

func TestStoreGetRedirect(t *testing.T) {
	// Setup the server to redirect the client to a new location
	resp := createMockResponse(t)
	mock := &nettest.MockHttpClient{}
	mock.SetResponse(resp, nil)

	s := New("http://localhost", 8080, mock)

	ref := upspin.Reference{
		Key:      "XX some hash XX",
		Protocol: upspin.HTTP,
	}
	loc := upspin.Location{
		Reference: ref,
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
		t.Fatalf("Server gave us wrong location. Expected: %v, got: %v", newLocation, locs[0])
	}
}

func createMockResponse(t *testing.T) *http.Response {
	newLoc, err := json.Marshal(newLocation)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	header := http.Header{}
	header.Add("Content-Type", "application/json")
	header.Add("Content-Length", fmt.Sprintf("%d", len(newLoc)))
	resp := http.Response{
		Status:     "200",
		StatusCode: 200,
		Header:     header,
		Body:       nettest.NewStringBufferReadCloser(string(newLoc)),
	}
	return &resp
}
