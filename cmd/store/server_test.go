package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	Key      = "1234"
	Contents = "contents of our file"
)

var (
	fileCache = cache.NewFileCache("")
)

func TestDelete(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete?ref=foo", nil)

	ss := newStoreServer()
	ss.deleteHandler(nil, resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"invalid ref"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete", nil)

	ss := newStoreServer()
	ss.deleteHandler(nil, resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidRequestType(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Delete only accepts POST HTTP requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/delete?ref=foo", nil)

	ss := newStoreServer()
	ss.deleteHandler(nil, resp, req)
	resp.Verify(t)
}

func TestGetRemoteFile(t *testing.T) {
	const RetLink = "http://here-you-go.com"
	loc := upspin.Location{}
	loc.Reference.Key = RetLink
	loc.Endpoint.Transport = upspin.GCP
	locJSON, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("Error marshalling: %v", err)
	}
	resp := nettest.NewExpectingResponseWriter(string(locJSON))
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%v", Key), nil)

	ss := newStoreServer()
	ss.cloudClient = &gcptest.ExpectGetGCP{Ref: Key, Link: RetLink}

	ss.getHandler(nil, resp, req)
	resp.Verify(t)
}

func TestGetLocalFile(t *testing.T) {
	// Seed a file into the server's cache
	err := fileCache.Put(Key, strings.NewReader(Contents))
	if err != nil {
		t.Fatalf("Error writing to cache: %v", err)
	}
	defer fileCache.Purge(Key) // cleanup after ourselves

	resp := nettest.NewExpectingResponseWriter(Contents)
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%v", Key), nil)

	ss := newStoreServer()

	ss.getHandler(nil, resp, req)
	resp.Verify(t)

}

func TestPutError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"putHandler: request Content-Type isn't multipart/form-data"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", nil)

	ss := newStoreServer()

	ss.putHandler(nil, resp, req)
	resp.Verify(t)
}

func TestPut(t *testing.T) {
	// Prepare a request
	bufFrom := bytes.NewBuffer([]byte(Contents))
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "dummy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(fw, bufFrom)
	if err != nil {
		t.Fatal(err)
	}
	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(netutil.Post, "http://localhost:8080/put", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(netutil.ContentType, w.FormDataContentType())
	resp := nettest.NewExpectingResponseWriter(`{"Key":"978F93921702F861CF941AAACE56B83AE17C8F6845FD674263FFF374A2696A4F"}`)

	ss := newStoreServer()

	ss.putHandler(nil, resp, req)
	resp.Verify(t)
}

func TestMain(m *testing.M) {
	m.Run()
	fileCache.Delete()
}

func newStoreServer() *storeServer {
	return &storeServer{
		cloudClient: &gcptest.DummyGCP{},
		fileCache:   fileCache,
	}
}
