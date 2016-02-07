// Package nettest implements helper functions for writing http tests
package nettest

import (
	"io"
	"io/ioutil"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
)

// ExpectingResponseWriter mimics a ResponseWriter and compares an
// expected response with the actual response written by callers.
type ExpectingResponseWriter struct {
	expectedResponse string
	response         string
	header           http.Header
}

// Write implements io.Writer.
func (e *ExpectingResponseWriter) Write(response []byte) (n int, err error) {
	e.response = string(response)
	return len(response), nil
}

// Header returns the http header.
func (e *ExpectingResponseWriter) Header() http.Header {
	return e.header
}

// WriteHeader writes the status code.
func (e *ExpectingResponseWriter) WriteHeader(int) {
}

// Verify checks that the response expected is the same as the one
// received. If they're different, it logs the error to the output of
// the test.
func (e *ExpectingResponseWriter) Verify(t *testing.T) {
	if e.expectedResponse != e.response {
		t.Errorf("Expected %v got %v", e.expectedResponse, e.response)
	}
}

// NewExpectingResponseWriter creates a new object with the expected response.
func NewExpectingResponseWriter(expected string) *ExpectingResponseWriter {
	resp := &ExpectingResponseWriter{
		header:           make(http.Header),
		expectedResponse: expected,
	}
	return resp
}

// VerifyRequests checkes that the expected and received requests are
// equivalent, by checking the URL fields, type of request (GET/POST)
// and payload, if any. It calls Fatal and Error on t if any mismatches are
// encountered.
func VerifyRequests(t *testing.T, expected, received []*http.Request) {
	if len(expected) != len(received) {
		t.Fatalf("Length of expected requests does not match. Expected %d, got %d", len(expected), len(received))
	}
	for i, e := range expected {
		r := received[i]
		if e.Method != r.Method {
			t.Errorf("Request method mismatch. Expected %v, got %v", e.Method, r.Method)
		}
		if e.URL.Host != r.URL.Host {
			t.Errorf("URL host mismatch. Expected %v, got %v", e.URL.Host, r.URL.Host)
		}
		if e.URL.Path != r.URL.Path {
			t.Errorf("URL path mismatch. Expected %v, got %v", e.URL.Path, r.URL.Path)
		}
		if e.URL.RawQuery != r.URL.RawQuery {
			t.Errorf("Query mismatch. Expected %v, got %v", e.URL.RawQuery, r.URL.RawQuery)
		}
		if e.Header.Get(netutil.ContentType) != r.Header.Get(netutil.ContentType) {
			t.Errorf("Content type mismatch. Expected %v, got %v", e.Header.Get(netutil.ContentType), r.Header.Get(netutil.ContentType))
		}
		if e.ContentLength != r.ContentLength {
			t.Errorf("Content length mismatch. Expected %v, got %v", e.ContentLength, r.ContentLength)
		}
		compareBytes(t, e.Body, r.Body)
	}
}

// compareBytes is a helper function to verifyRequests that compares
// that the body of two HTTP requests are identical.
func compareBytes(t *testing.T, expectedBody, receivedBody io.ReadCloser) {
	if expectedBody == nil && receivedBody == nil {
		// No body is a match
		return
	}
	if expectedBody != nil && receivedBody == nil {
		t.Fatalf("Expected body contained data, but received no data")
	}
	if expectedBody == nil && receivedBody != nil {
		t.Fatalf("Received non-empty body, but expected empty")
	}
	// Compare the actual bytes
	defer expectedBody.Close()
	defer receivedBody.Close()
	e, err := ioutil.ReadAll(expectedBody)
	if err != nil {
		t.Fatalf("Error reading expected body: %v", err)
	}
	r, err := ioutil.ReadAll(receivedBody)
	if err != nil {
		t.Fatalf("Error reading received body: %v", err)
	}
	if len(e) != len(r) {
		t.Fatalf("Request body length mismatch. Expected %v, got %v", len(e), len(r))
	}
	mismatch := 0
	for i, b := range e {
		if b != r[i] {
			mismatch = mismatch + 1
		}
	}
	if mismatch > 0 {
		t.Errorf("Body contents mismatch. Number of mismatched bytes: %d", mismatch)
	}
}
