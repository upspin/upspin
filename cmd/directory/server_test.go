package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

func Put(t *testing.T, dirEntry upspin.DirEntry, errorExpected string) {
	resp := nettest.NewExpectingResponseWriter(errorExpected)
	jsonStr, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("Can't marshal dirEntry: %v", err)
	}
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBuffer(jsonStr))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	putHandler(resp, req)
	resp.Verify(t)
}

func TestPutErrorParseRoot(t *testing.T) {
	// No path given
	Put(t, upspin.DirEntry{}, `{"error":"dir entry verification failed: no slash in path"}`)
}

func TestPutErrorParseUser(t *testing.T) {
	dir := upspin.DirEntry{
		Name: upspin.PathName("a@x/myroot/myfile"),
	}
	Put(t, dir, `{"error":"dir entry verification failed: no user name in path"}`)
}

func makeValidMeta() upspin.Metadata {
	return upspin.Metadata{
		IsDir:    true,
		Sequence: 0,
	}
}

func TestPutErrorInvalidSequenceNumber(t *testing.T) {
	meta := makeValidMeta()
	meta.Sequence = -1
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: meta,
	}
	Put(t, dir, `{"error":"dir entry verification failed: invalid sequence number"}`)
}

func TestLookupPathError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"missing pathname in request"}`)
	req, err := http.NewRequest("GET", "http://localhost:8080/get", nil)
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	getHandler(resp, req)
	resp.Verify(t)
}

// From here on, we need a connection to GCP
func ConnectedPut(t *testing.T, dirEntry upspin.DirEntry, errorExpected string) {
	// Re-using the same bucket is dangerous because of leftover
	// state and collision with multiple tests. I will fix this
	// soon.
	// TODO(edpin): fix usage of same bucket for tests.
	configureCloudClient("upspin", "upspin-test")
	Put(t, dirEntry, errorExpected)
}

func TestPutErrorFileNoDir(t *testing.T) {
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: makeValidMeta(),
	}
	ConnectedPut(t, dir, `{"error":"path is not writable"}`)
}

func TestLookupPathNotFound(t *testing.T) {
	configureCloudClient("upspin", "upspin-test")
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: pathname not found"}`)
	req, err := http.NewRequest("GET", "http://localhost:8080/get?pathname=o@foo.bar/invalid/invalid/invalid", nil)
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	getHandler(resp, req)
	resp.Verify(t)
}

// Further connected tests require that we fix the TODO above, which
// requires the delete operation.
