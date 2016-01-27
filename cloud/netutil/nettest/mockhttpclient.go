package nettest

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
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
		requests:  make([]*http.Request, 5),
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
	header.Add(netutil.ContentLength, fmt.Sprintf("%d", len(data)))
	status := fmt.Sprintf("%d", statusCode)
	resp := &http.Response{
		Status:     status,
		StatusCode: statusCode,
		Header:     header,
		Body:       newStringBufferReadCloser(string(data)),
	}
	return MockHTTPResponse{Error: nil, Response: resp}
}

// Request returns the request sent to the http client.
func (m *MockHTTPClient) Requests() []*http.Request {
	return m.requests
}

func (m *MockHTTPClient) Do(request *http.Request) (resp *http.Response, err error) {
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		log.Fatal("Not enough mock responses exist")
	}
	toReply := m.responses[0]
	m.responses = m.responses[1:]
	return toReply.Response, toReply.Error
}

// stringBufferReadCloser is a buffer for a string that implements the
// ReadCloser interface.
type stringBufferReadCloser struct {
	str *strings.Reader
}

func (sb *stringBufferReadCloser) Close() error {
	return nil
}

func (sb *stringBufferReadCloser) Read(b []byte) (n int, err error) {
	return sb.str.Read(b)
}

func newStringBufferReadCloser(str string) *stringBufferReadCloser {
	return &stringBufferReadCloser{strings.NewReader(str)}
}
