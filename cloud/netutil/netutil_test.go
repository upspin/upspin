package netutil

import (
	"bytes"
	"io"
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
		t.Errorf("Expected '%q' got '%q'", e.expectedResponse, e.response)
	}
}

func NewExpectingResponseWriter(expected string) *ExpectingResponseWriter {
	resp := &ExpectingResponseWriter{}
	resp.header = make(http.Header)
	resp.expectedResponse = expected
	return resp
}

func TestSendJSONErrorString(t *testing.T) {
	resp := NewExpectingResponseWriter("{error:\"Something bad happened\"}")
	SendJSONErrorString(resp, "Something bad happened")
	resp.Verify(t)
}

func TestSendJSONError(t *testing.T) {
	resp := NewExpectingResponseWriter("{error:\"error reading:EOF\"}")
	SendJSONError(resp, "error reading:", io.EOF)
	resp.Verify(t)
}

func TestSendJSONReply(t *testing.T) {
	resp := NewExpectingResponseWriter("{\"A\":\"foo\",\"B\":32}")
	SendJSONReply(resp, &struct {
		A string
		B int
	}{A: "foo", B: 32})
	resp.Verify(t)
}

var (
	data = "some data"
)

func TestBufferRequest(t *testing.T) {
	resp := NewExpectingResponseWriter("") // Nothing is sent
	req, _ := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBufferString(data))
	buf := BufferRequest(resp, req, 1000)
	if len(buf) != len(data) {
		t.Fatalf("Buffer size mismatch")
	}
	if string(buf) != data {
		t.Fatalf("Expected '%q' got '%q'", data, string(buf))
	}
	resp.Verify(t)
}

func TestBufferRequestTooBig(t *testing.T) {
	resp := NewExpectingResponseWriter("{error:\"Invalid request\"}") // Request is too big
	req, _ := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBufferString(data))
	buf := BufferRequest(resp, req, 5)
	if len(buf) > 5 {
		t.Fatalf("Buffer size mismatch")
	}
	resp.Verify(t)
}
