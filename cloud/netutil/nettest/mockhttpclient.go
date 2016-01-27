package nettest

import (
	"net/http"
	"strings"
)

// MockHttpClient is a simple http client that saves the Request given
// to it and always responds with the preset Response. (In an ideal
// world, we'd compare if expectations match and then issue the
// correct response as a real mock. We're not doing this here. Yet.)
type MockHttpClient struct {
	http.Client
	request  *http.Request
	response *http.Response
	error    error
}

// SetResponse makes the http client respond with the given response.
func (m *MockHttpClient) SetResponse(response *http.Response, err error) {
	m.response = response
	m.error = err
}

// Request returns the request sent to the http client.
func (m *MockHttpClient) Request() *http.Request {
	return m.request
}

func (m *MockHttpClient) Do(request *http.Request) (resp *http.Response, err error) {
	m.request = request
	return m.response, m.error
}

// StringBufferReadCloser is a buffer for a string that implements the
// ReadCloser interface.
type StringBufferReadCloser struct {
	str *strings.Reader
}

func (sb *StringBufferReadCloser) Close() error {
	return nil
}

func (sb *StringBufferReadCloser) Read(b []byte) (n int, err error) {
	return sb.str.Read(b)
}

func NewStringBufferReadCloser(str string) *StringBufferReadCloser {
	return &StringBufferReadCloser{strings.NewReader(str)}
}
