package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

func Put(t *testing.T, ds *DirServer, dirEntry upspin.DirEntry, errorExpected string) {
	resp := nettest.NewExpectingResponseWriter(errorExpected)
	jsonStr, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("Can't marshal dirEntry: %v", err)
	}
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBuffer(jsonStr))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	ds.putHandler(resp, req)
	resp.Verify(t)
}

func TestPutErrorParseRoot(t *testing.T) {
	// No path given
	Put(t, newDirServer(), upspin.DirEntry{}, `{"error":"dir entry verification failed: no slash in path"}`)
}

func TestPutErrorParseUser(t *testing.T) {
	dir := upspin.DirEntry{
		Name: upspin.PathName("a@x/myroot/myfile"),
	}
	Put(t, newDirServer(), dir, `{"error":"dir entry verification failed: no user name in path"}`)
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
	Put(t, newDirServer(), dir, `{"error":"dir entry verification failed: invalid sequence number"}`)
}

func TestLookupPathError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"missing pathname in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get", nil)

	ds := newDirServer()
	ds.getHandler(resp, req)
	resp.Verify(t)
}

func TestListMissingPrefix(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"missing prefix in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/list", nil)

	ds := newDirServer()
	ds.listHandler(resp, req)
	resp.Verify(t)
}

func TestListBadPath(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"list: bad user name in path"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/list?prefix=missing/email/dir/file", nil)

	ds := newDirServer()
	ds.listHandler(resp, req)
	resp.Verify(t)
}

func TestPutErrorFileNoDir(t *testing.T) {
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: makeValidMeta(),
	}
	egcp := &gcptest.ExpectGetGCP{
		Ref: "something that does not match",
	}

	ds := new(egcp, &http.Client{})
	Put(t, ds, dir, `{"error":"path is not writable"}`)
}

func TestLookupPathNotFound(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: pathname not found"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get?pathname=o@foo.bar/invalid/invalid/invalid", nil)
	egcp := &gcptest.ExpectGetGCP{
		Ref: "something that does not match",
	}

	ds := new(egcp, &http.Client{})
	ds.getHandler(resp, req)
	resp.Verify(t)
}

func TestList(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"Names":["testuser@google.com/subdir/","testuser@google.com/subdir/test.txt"]}`)
	req, err := http.NewRequest("GET", "http://localhost:8080/list?prefix=testuser@google.com/sub", nil)
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	lgcp := &listGCP{
		prefix:    "testuser@google.com/sub",
		fileNames: []string{"testuser@google.com/subdir/", "testuser@google.com/subdir/test.txt"},
		fileLinks: []string{"http://a.com", "http://b.com"},
	}
	ds := new(lgcp, &http.Client{})
	ds.listHandler(resp, req)
	resp.Verify(t)
}

func TestPutNotDir(t *testing.T) {
	// The DirEntry we're trying to Put
	dir := upspin.DirEntry{
		Name: upspin.PathName("test@foo.com/mydir/myfile.txt"),
	}
	dirEntryJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The DirEntry of the parent
	dirParent := upspin.DirEntry{
		Name: upspin.PathName("test@foo.com/mydir"),
		Metadata: upspin.Metadata{
			IsDir: false, // Not a directory
		},
	}
	dirParentJSON, err := json.Marshal(dirParent)
	if err != nil {
		t.Fatal(err)
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"path is not writable"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	const downloadLink = "http://download-it-here.com/mydir"
	egcp := &gcptest.ExpectGetGCP{
		Ref:  "test@foo.com/mydir",
		Link: downloadLink,
	}

	// Setup a mock HTTP client that will return our DirEntry dir.
	mockHTTPClient := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{nettest.NewMockHTTPResponse(200, "application/json", dirParentJSON)},
		[]*http.Request{nettest.NewRequest(t, netutil.Get, downloadLink, nil)})

	ds := new(egcp, mockHTTPClient)
	ds.putHandler(resp, req)
	resp.Verify(t)
	mockHTTPClient.Verify(t)
}

func TestPut(t *testing.T) {
	// The DirEntry we're trying to Put
	dir := upspin.DirEntry{
		Name: upspin.PathName("test@foo.com/mydir/myfile.txt"),
	}
	dirEntryJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The DirEntry of the parent
	dirParent := upspin.DirEntry{
		Name: upspin.PathName("test@foo.com/mydir"),
		Metadata: upspin.Metadata{
			IsDir: true,
		},
	}
	dirParentJSON, err := json.Marshal(dirParent)
	if err != nil {
		t.Fatal(err)
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	const downloadLink = "http://download-it-here.com/mydir"
	egcp := &gcptest.ExpectGetGCP{
		Ref:  "test@foo.com/mydir",
		Link: downloadLink,
	}

	// Setup a mock HTTP client that will return our DirEntry dir.
	mockHTTPClient := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{nettest.NewMockHTTPResponse(200, "application/json", dirParentJSON)},
		[]*http.Request{nettest.NewRequest(t, netutil.Get, downloadLink, nil)})

	ds := new(egcp, mockHTTPClient)
	ds.putHandler(resp, req)
	resp.Verify(t)
	mockHTTPClient.Verify(t)
}

func newDirServer() *DirServer {
	return new(&gcptest.DummyGCP{}, &http.Client{})
}

// listGCP is a DummyGCP that returns a slice of fileNames and
// fileLinks if a call to List matches the expected prefix
type listGCP struct {
	gcptest.DummyGCP
	prefix    string
	fileNames []string
	fileLinks []string
}

func (l *listGCP) List(prefix string) (name []string, link []string, err error) {
	if l.prefix == prefix {
		return l.fileNames, l.fileLinks, nil
	}
	return []string{}, []string{}, errors.New("Not found")
}
