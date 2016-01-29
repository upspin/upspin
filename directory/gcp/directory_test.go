package directory

import (
	"encoding/json"
	"errors"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	pathName = "bob@jones.com/myroot/mysubdir"
)

var (
	errBadConnection      = errors.New("bad internet connection")
	errMkdirBadConnection = newError("MakeDirectory", pathName, errBadConnection)
	reference             = upspin.Reference{
		Key:     "the key",
		Packing: upspin.HTTP,
	}
	location = upspin.Location{
		Reference: reference,
	}
)

func TestMkdirError(t *testing.T) {
	// The server will error out.
	resp := nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})

	d := New("http://localhost:8080", mock)

	_, err := d.MakeDirectory(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errMkdirBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errMkdirBadConnection, err)
	}
}

func TestMkdir(t *testing.T) {
	mock := nettest.NewMockHTTPClient(createMockResponse(t))

	d := New("http://localhost:8080", mock)

	loc, err := d.MakeDirectory(upspin.PathName(pathName))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if loc != location {
		t.Fatalf("Location differs. Expected %v, got %v", location, loc)
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
	if u.Path != "/put" {
		t.Fatalf("Expected request to /put, got %v", u.Path)
	}
	if u.RawQuery != "" {
		t.Fatalf("Expected no query, got %v", u.RawQuery)
	}
	if reqs[0].ContentLength < int64(len(pathName)) {
		t.Fatalf("Request body too small. Expect at least %d, got %d", len(pathName), reqs[0].ContentLength)
	}
}

func createMockResponse(t *testing.T) []nettest.MockHTTPResponse {
	loc, err := json.Marshal(location)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", loc)
	return []nettest.MockHTTPResponse{resp}
}
