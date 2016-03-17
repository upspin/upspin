// Package nettest implements helper functions for writing http tests
package nettest

import (
	"net/http"
	"testing"
)

// ExpectingResponseWriter mimics a ResponseWriter and compares an
// expected response with the actual response written by callers.
type ExpectingResponseWriter struct {
	expectedResponse string
	response         string
	expectedCode     int
	code             int
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
func (e *ExpectingResponseWriter) WriteHeader(code int) {
	e.code = code
}

// Verify checks that the response expected is the same as the one
// received. If they're different, it logs the error to the output of
// the test.
func (e *ExpectingResponseWriter) Verify(t *testing.T) {
	if e.expectedResponse != e.response {
		t.Errorf("Expected %v got %v", e.expectedResponse, e.response)
	}
	if e.expectedCode != e.code {
		t.Errorf("Expected code %d got %d", e.expectedCode, e.code)
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

// NewExpectingResponseWriterWithCode creates a new object with the expected code and response body.
func NewExpectingResponseWriterWithCode(expectedCode int, expectedBody string) *ExpectingResponseWriter {
	resp := &ExpectingResponseWriter{
		header:           make(http.Header),
		expectedCode:     expectedCode,
		expectedResponse: expectedBody,
	}
	return resp
}
