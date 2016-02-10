package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
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

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"invalid ref"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidRequestType(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Delete only accepts POST HTTP requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/delete?ref=foo", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestGetRemoteFile(t *testing.T) {
	const RetLink = "http://here-you-go.com"
	loc := upspin.Location{}
	loc.Reference.Key = RetLink
	loc.Endpoint.Transport = upspin.HTTP
	locJSON, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("Error marshalling: %v", err)
	}
	resp := nettest.NewExpectingResponseWriter(string(locJSON))
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?ref=%v", Key), nil)

	ss := NewStoreServer()
	ss.cloudClient = &expectGetGCP{ref: Key, link: RetLink}

	ss.getHandler(resp, req)
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

	ss := NewStoreServer()

	ss.getHandler(resp, req)
	resp.Verify(t)

}

func TestPutError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"putHandler: request Content-Type isn't multipart/form-data"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", nil)

	ss := NewStoreServer()

	ss.putHandler(resp, req)
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
	resp := nettest.NewExpectingResponseWriter(`{"Key":"978f93921702f861cf941aaace56b83ae17c8f6845fd674263fff374a2696a4f"}`)

	ss := NewStoreServer()

	ss.putHandler(resp, req)
	resp.Verify(t)
}

func TestMain(m *testing.M) {
	m.Run()
	cache.DeleteFileCache(fileCache)
}

// dummyGCP is a dummy version of gcp.Interface that does nothing.
type dummyGCP struct {
}

var _ gcp.Interface = (*dummyGCP)(nil)

func NewStoreServer() *StoreServer {
	return &StoreServer{
		cloudClient: &dummyGCP{},
		fileCache:   fileCache,
	}
}

func (m *dummyGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", nil
}

func (m *dummyGCP) Get(ref string) (link string, error error) {
	return "", nil
}

func (m *dummyGCP) Put(ref string, contents []byte) (refLink string, error error) {
	return "", nil
}

func (m *dummyGCP) List(prefix string) (name []string, link []string, err error) {
	return []string{}, []string{}, nil
}

func (m *dummyGCP) Delete(ref string) error {
	return nil
}

func (m *dummyGCP) Connect() {
}

// expectGetGCP is a dummyGCP that expects Get will be called with a
// given ref and when it does, it replies with the preset link.
type expectGetGCP struct {
	dummyGCP
	ref  string
	link string
}

func (e *expectGetGCP) Get(ref string) (link string, error error) {
	if ref == e.ref {
		return e.link, nil
	}
	return "", errors.New("not found")
}
