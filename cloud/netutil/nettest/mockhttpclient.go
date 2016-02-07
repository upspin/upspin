package nettest

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
)

var (
	AnyRequest = NewRequest(nil, "*", "*", []byte("*"))
)

// MockHTTPClient is a simple HTTP client that saves the Request given
// to it and always responds with the preset Response. (In an ideal
// world, we'd compare if expectations match and then issue the
// correct response as a real mock. We're not doing this here. Yet.)
// TODO(edpin): investigate how to do proper mock-style matching.
type MockHTTPClient struct {
	http.Client
	requests  []*http.Request
	responses []MockHTTPResponse
}

// MockHTTPResponse contains either an error or an actual
// http.Response.
type MockHTTPResponse struct {
	Error    error
	Response *http.Response
}

// NewMockHTTPClient creates an instance pre-loaded with the responses
// that will be returned when Do() is invoked on the HTTP client, in order.
// To make a new Response, use helper method NewMockHTTPResponse below.
func NewMockHTTPClient(responsesToSend []MockHTTPResponse) *MockHTTPClient {
	return &MockHTTPClient{
		requests:  make([]*http.Request, 0, 5),
		responses: responsesToSend,
	}
}

// NewMockHTTPResponse creates a MockHTTPResponse with a nil error and
// a minimal http.Response that contains a given status code, a body
// type (such as "text/html", "application/json") and
// contents. Manipulate the Response field of the returned object if
// necessary to fine-tune it.
func NewMockHTTPResponse(statusCode int, bodyType string, data []byte) MockHTTPResponse {
	header := http.Header{}
	header.Add(netutil.ContentType, bodyType)
	header.Add(netutil.ContentLength, fmt.Sprint(len(data)))
	status := fmt.Sprint(statusCode)
	resp := &http.Response{
		Status:     status,
		StatusCode: statusCode,
		Header:     header,
		Body:       &readCloser{bytes.NewReader(data)},
	}
	return MockHTTPResponse{Error: nil, Response: resp}
}

// Request returns the request sent to the http client.
func (m *MockHTTPClient) Requests() []*http.Request {
	return m.requests
}

// Do is analogous to HTTPClient.Do and satisfies HTTPClientInterface.
func (m *MockHTTPClient) Do(request *http.Request) (resp *http.Response, err error) {
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		log.Fatal("Not enough mock responses exist")
	}
	toReply := m.responses[0]
	m.responses = m.responses[1:]
	return toReply.Response, toReply.Error
}

// TestingInterface is a simplified version of testing.T. In tests,
// just pass a real *testing.T.
type TestingInterface interface {
	// Errorf logs an error and continues execution
	Errorf(format string, args ...interface{})

	// Fatalf logs an error, marks it as fatal and continues execution
	Fatalf(format string, args ...interface{})
}

// Verify checks that all expected and all received requests are
// equivalent, by checking their URL fields, type of request
// (GET/POST) and payload, if any. It calls Fatal and Error on t if
// any mismatches are encountered.
//
// TODO(edpin): t and expected should be provided at constructor time
// and received. In order to make this change, we have to refactor
// some other tests and this will come in another CL.
func (m *MockHTTPClient) Verify(t TestingInterface, expected []*http.Request) {
	received := m.Requests()
	if len(expected) != len(received) {
		t.Fatalf("Length of expected requests does not match. Expected %d, got %d", len(expected), len(received))
		return
	}
	for i, e := range expected {
		// Short-circuit the rest since we know AnyRequest is all wildcards.
		if e == AnyRequest {
			continue
		}
		r := received[i]
		if e.Method != "*" && e.Method != r.Method {
			t.Errorf("Request method mismatch. Expected %v, got %v", e.Method, r.Method)
		}
		// If Path is a wildcard, ignore everything else about the URL
		if e.URL.Path != "*" {
			if e.URL.Scheme != r.URL.Scheme {
				t.Errorf("Scheme mismatch. Expected %v, got %v", e.URL.Scheme, r.URL.Scheme)
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
		}
		isWildCard := compareBytes(t, e.Body, r.Body)
		if !isWildCard {
			if e.Header.Get(netutil.ContentType) != r.Header.Get(netutil.ContentType) {
				t.Errorf("Content type mismatch. Expected %v, got %v", e.Header.Get(netutil.ContentType), r.Header.Get(netutil.ContentType))
			}
			// This is probably unnecessary as
			// compareBytes has already compared lengths
			// in the body. But to ensure the request was
			// created properly, we still check it.
			if e.ContentLength != r.ContentLength {
				t.Errorf("Content length mismatch. Expected %v, got %v", e.ContentLength, r.ContentLength)
			}
		}
	}
}

// compareBytes is a helper function to Verify() that compares that
// the body of two HTTP requests are identical. It may not return, but
// if it does, it returns whether the expected content was a wildcard.
func compareBytes(t TestingInterface, expectedBody, receivedBody io.ReadCloser) bool {
	if expectedBody == nil && receivedBody == nil {
		// No body is a match
		return false
	}
	var e []byte // expected contents
	var err error
	if expectedBody != nil {
		defer expectedBody.Close()
		e, err = ioutil.ReadAll(expectedBody)
		if err != nil {
			t.Fatalf("Error reading expected body: %v", err)
			return false
		}
	}
	if len(e) == 1 && string(e[0]) == "*" {
		// a "*" matches anything
		return true
	}
	if expectedBody == nil && receivedBody != nil {
		t.Fatalf("Received non-empty body, but expected empty")
		return false
	}
	// Compare the actual bytes
	defer receivedBody.Close()
	r, err := ioutil.ReadAll(receivedBody)
	if err != nil {
		t.Fatalf("Error reading received body: %v", err)
		return false
	}
	if len(e) != len(r) {
		t.Fatalf("Request body length mismatch. Expected %v, got %v", len(e), len(r))
		return false
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
	return false
}

// NewRequest is a convenience function to create an HTTP request of a given type with a given payload.
func NewRequest(t TestingInterface, reqType, request string, payload []byte) *http.Request {
	var b io.Reader
	if payload != nil {
		b = bytes.NewBuffer(payload)
	}
	r, err := http.NewRequest(reqType, request, b)
	if err != nil {
		t.Fatalf("Error creating a request: %v", err)
	}
	return r
}

// readCloser adds a Close method to a reader.
type readCloser struct {
	io.Reader
}

func (r *readCloser) Close() error {
	return nil
}
