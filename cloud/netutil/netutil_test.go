package netutil_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
)

func TestSendJSONErrorString(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Something bad happened"}`)
	netutil.SendJSONErrorString(resp, "Something bad happened")
	resp.Verify(t)
}

func TestSendJSONError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"error reading:EOF"}`)
	netutil.SendJSONError(resp, "error reading:", io.EOF)
	resp.Verify(t)
}

func TestSendJSONReply(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"A":"foo","B":32}`)
	netutil.SendJSONReply(resp, &struct {
		A string
		B int
	}{A: "foo", B: 32})
	resp.Verify(t)
}

var (
	data = "some data"
)

func TestBufferRequest(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter("") // Nothing is sent
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBufferString(data))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	buf := netutil.BufferRequest(resp, req, 1000)
	if len(buf) != len(data) {
		t.Fatalf("Buffer size mismatch. Expected %d got %d", len(data), len(buf))
	}
	if string(buf) != data {
		t.Fatalf("Expected %q got %q", data, string(buf))
	}
	resp.Verify(t)
}

func TestBufferRequestTooBig(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"invalid request"}`) // Request is too big
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBufferString(data))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	buf := netutil.BufferRequest(resp, req, 5)
	if len(buf) > 5 {
		t.Fatalf("Buffer size bigger overflow. Max was 5, got: %d", len(buf))
	}
	resp.Verify(t)
}
