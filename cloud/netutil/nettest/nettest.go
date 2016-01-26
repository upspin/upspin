// Package nettest implements helper functions for writing http tests
package nettest

import (
	"net/http"
	"testing"
)

type ExpectingResponseWriter struct {
	expectedResponse string
	response         string
	header           http.Header
}

func (e *ExpectingResponseWriter) Write(response []byte) (n int, err error) {
	e.response = string(response)
	return len(response), nil
}

func (e *ExpectingResponseWriter) Header() http.Header {
	return e.header
}

func (e *ExpectingResponseWriter) WriteHeader(int) {
}

func (e *ExpectingResponseWriter) Verify(t *testing.T) {
	if e.expectedResponse != e.response {
		t.Errorf("Expected %q got %q", e.expectedResponse, e.response)
	}
}

func NewExpectingResponseWriter(expected string) *ExpectingResponseWriter {
	resp := &ExpectingResponseWriter{
		header:           make(http.Header),
		expectedResponse: expected,
	}
	return resp
}
