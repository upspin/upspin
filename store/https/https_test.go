package https

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"strings"

	"upspin.io/cloud/netutil/nettest"
)

const (
	contents        = "contents of a ref"
	ref             = "https://hello.com/someref"
	errSomethingBad = "something went bad"
)

func TestGetError(t *testing.T) {
	resp := nettest.MockHTTPResponse{
		Error:    errors.New(errSomethingBad),
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp}, []*http.Request{nettest.AnyRequest})
	s := New("http://localhost:80", mock)

	_, _, err := s.Get(ref)

	expected := fmt.Sprintf("https: store error: Get: %s: %s", ref, errSomethingBad)
	if err.Error() != expected {
		t.Fatalf("Server reply failed: expected %v got %v", expected, err)
	}

	mock.Verify(t)
}

func TestGet(t *testing.T) {
	resp := nettest.NewMockHTTPResponse(http.StatusOK, "binary", []byte(contents))
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp}, []*http.Request{nettest.AnyRequest})
	s := New("http://localhost:80", mock)

	data, _, err := s.Get(ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contents {
		t.Errorf("Expected contents %q, got %q", contents, data)
	}

	mock.Verify(t)
}

func TestPut(t *testing.T) {
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{}, []*http.Request{})
	s := New("http://localhost:80", mock)

	_, err := s.Put([]byte(contents))
	if err == nil {
		t.Fatal("Expected error")
	}
	const expectedError = "https: Put: not implemented"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %s, got %s", expectedError, err)
	}
	mock.Verify(t)
}

func TestDelete(t *testing.T) {
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{}, []*http.Request{})
	s := New("http://localhost:80", mock)

	err := s.Delete(ref)
	if err == nil {
		t.Fatal("Expected error")
	}
	const expectedError = "https: Delete: not implemented"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %s, got %s", expectedError, err)
	}
	mock.Verify(t)
}
