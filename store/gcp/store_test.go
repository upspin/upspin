package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
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

	errInvalidKey = NewStoreError(invalidKeyError, "")
)

func TestStorePutError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errSomethingBad),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp}, []*http.Request{nettest.AnyRequest})
	s := newStore("http://localhost:8080", mock)

	_, err := s.Put([]byte("contents"))

	expected := fmt.Sprintf(serverError, errSomethingBad)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}

	mock.Verify(t)
}

func TestStorePut(t *testing.T) {
	// The server will respond with a location for the object.
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", []byte("*"))
	mock := nettest.NewMockHTTPClient(createMockPutResponse(t), []*http.Request{req})
	s := newStore("http://localhost:8080", mock)

	contents := []byte("contents")
	key, err := s.Put(contents)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if key != contentKey {
		t.Fatalf("Server gave us wrong location. Expected %v, got %v", contentKey, key)
	}
	// Verify the server received the proper request
	mock.Verify(t)

	// Further ensure we sent the right number of bytes
	bytesSent := mock.Requests()[0].ContentLength
	if bytesSent != 245 {
		t.Errorf("Wrong number of bytes sent. Expected 245, got %v", bytesSent)
	}
}

func TestStoreGetError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errBrokenPipe),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp}, []*http.Request{nettest.AnyRequest})
	s := newStore("http://localhost:8080", mock)

	_, _, err := s.Get("1234")

	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}
	expected := fmt.Sprintf(serverError, errBrokenPipe)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
	mock.Verify(t)
}

func TestStoreGetErrorEmptyKey(t *testing.T) {
	// Our request is invalid.
	mock := nettest.NewMockHTTPClient(nil, nil)
	s := newStore("http://localhost:8080", mock)

	_, _, err := s.Get("")

	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}
	expected := invalidKeyError
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}
}

func TestStoreGetRedirect(t *testing.T) {
	// The server will redirect the client to a new location
	const LookupKey = "XX some hash XX"
	mock := nettest.NewMockHTTPClient(createMockGetResponse(t), []*http.Request{
		nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%s", LookupKey), nil),
	})

	s := newStore("http://localhost:8080", mock)

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
	mock.Verify(t)
}

func TestStoreDeleteInvalidKey(t *testing.T) {
	// No requests are sent
	mock := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{},
		[]*http.Request{})

	s := newStore("http://localhost:8080", mock)
	err := s.Delete("")
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if err.Error() != errInvalidKey.Error() {
		t.Fatalf("Expected error %v, got %v", errInvalidKey, err)
	}
	mock.Verify(t)
}

func TestStoreDelete(t *testing.T) {
	const Key = "xyz"
	mock := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"error":"Success"}`))},
		[]*http.Request{nettest.NewRequest(t, netutil.Post, fmt.Sprintf("http://localhost:8080/delete?ref=%s", Key), nil)})

	s := newStore("http://localhost:8080", mock)
	err := s.Delete(Key)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	mock.Verify(t)
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
