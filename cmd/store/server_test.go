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

	"upspin.googlesource.com/upspin.git/auth/testauth"
	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	Ref      = "1234"
	Contents = "contents of our file"
)

var (
	fileCache = cache.NewFileCache("")
	session   = testauth.NewSessionForTesting("dude@foo.com", false, nil)
)

func TestDelete(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete?ref=foo", nil)

	ss := newStoreServer()
	ss.deleteHandler(session, resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidReference(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: invalid reference"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete", nil)

	ss := newStoreServer()
	ss.deleteHandler(session, resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidRequestType(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: Delete only accepts POST HTTP requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/delete?ref=foo", nil)

	ss := newStoreServer()
	ss.deleteHandler(session, resp, req)
	resp.Verify(t)
}

func TestGetInvalidReference(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: get: not found"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get?ref=foofoo", nil)

	ss := newStoreServer()
	ss.cloudClient = &gcptest.ExpectGetGCP{Ref: "does not match", Link: ""}

	ss.getHandler(session, resp, req)
	resp.Verify(t)
}

func TestGetCrazyGCP(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: get: invalid link returned from GCP: bad-url"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get?ref=foofoo", nil)

	ss := newStoreServer()
	ss.cloudClient = &gcptest.ExpectGetGCP{Ref: "foofoo", Link: "bad-url"}

	ss.getHandler(session, resp, req)
	resp.Verify(t)
}

func TestGetEmptyReference(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: invalid reference"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get", nil)

	ss := newStoreServer()

	ss.getHandler(session, resp, req)
	resp.Verify(t)
}

func TestGetRemoteFile(t *testing.T) {
	const RetLink = "http://here-you-go.com"
	loc := upspin.Location{}
	loc.Reference = RetLink
	loc.Endpoint.Transport = upspin.GCP
	locJSON, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("Error marshalling: %v", err)
	}
	resp := nettest.NewExpectingResponseWriter(string(locJSON))
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%v", Ref), nil)

	ss := newStoreServer()
	ss.cloudClient = &gcptest.ExpectGetGCP{Ref: Ref, Link: RetLink}

	ss.getHandler(session, resp, req)
	resp.Verify(t)
}

func TestGetLocalFile(t *testing.T) {
	// Seed a file into the server's cache
	err := fileCache.Put(Ref, strings.NewReader(Contents))
	if err != nil {
		t.Fatalf("Error writing to cache: %v", err)
	}
	defer fileCache.Purge(Ref) // cleanup after ourselves

	resp := nettest.NewExpectingResponseWriterWithCode(http.StatusOK, Contents)
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%v", Ref), nil)

	ss := newStoreServer()

	ss.getHandler(session, resp, req)
	resp.Verify(t)
}

func TestPutError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"StoreService: Put: request Content-Type isn't multipart/form-data"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", nil)

	ss := newStoreServer()

	ss.putHandler(session, resp, req)
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
	resp := nettest.NewExpectingResponseWriter(`{"Ref":"978F93921702F861CF941AAACE56B83AE17C8F6845FD674263FFF374A2696A4F"}`)

	ss := newStoreServer()

	ss.putHandler(session, resp, req)
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
