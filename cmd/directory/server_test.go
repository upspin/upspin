package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/auth/testauth"
	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	pathName       = "test@foo.com/mydir/myfile.txt"
	parentPathName = "test@foo.com/mydir"
)

var (
	dummySess = testauth.NewSessionForTesting("test@google.com", false, nil)
	dir       = upspin.DirEntry{
		Name: upspin.PathName(pathName),
		Metadata: upspin.Metadata{
			IsDir: false,
			Size:  32,
			Time:  upspin.Now(),
		},
	}
	dirParent = upspin.DirEntry{
		Name: upspin.PathName(parentPathName),
		Metadata: upspin.Metadata{
			IsDir: true,
		},
	}
)

func Put(t *testing.T, ds *dirServer, dirEntry upspin.DirEntry, errorExpected string) {
	resp := nettest.NewExpectingResponseWriter(errorExpected)
	jsonStr, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("Can't marshal dirEntry: %v", err)
	}
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBuffer(jsonStr))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	ds.putHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorParseRoot(t *testing.T) {
	// No path given
	Put(t, newDummyDirServer(), upspin.DirEntry{}, `{"error":"DirService: verifyDirEntry: no user name in path"}`)
}

func TestPutErrorParseUser(t *testing.T) {
	dir := upspin.DirEntry{
		Name: upspin.PathName("a@x/myroot/myfile"),
	}
	Put(t, newDummyDirServer(), dir, `{"error":"DirService: verifyDirEntry: a@x/myroot/myfile: no user name in path"}`)
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
	Put(t, newDummyDirServer(), dir, `{"error":"DirService: verifyMeta: fred@bob.com/myroot/myfile: invalid sequence number"}`)
}

func TestLookupPathError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: missing pathname in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get", nil)

	ds := newDummyDirServer()
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestListMissingPrefix(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: missing prefix in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/list", nil)

	ds := newDummyDirServer()
	ds.listHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestListBadPath(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: bad user name in path"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/list?prefix=missing/email/dir/file", nil)

	ds := newDummyDirServer()
	ds.listHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorFileNoDir(t *testing.T) {
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: makeValidMeta(),
	}
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: "something that does not match",
	}

	ds := newDirServer(egcp)
	Put(t, ds, dir, `{"error":"DirService: verify: fred@bob.com/myroot/myfile: parent path not found"}`)
}

func TestLookupPathNotFound(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: get: o@foo.bar/invalid/invalid/invalid: path not found"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get?pathname=o@foo.bar/invalid/invalid/invalid", nil)
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: "something that does not match",
	}

	ds := newDirServer(egcp)
	ds.getHandler(dummySess, resp, req)
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
	ds := newDirServer(lgcp)
	ds.listHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutNotDir(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The DirEntry of the parent, converted to JSON.
	notDirParent := dirParent
	notDirParent.Metadata.IsDir = false
	dirParentJSON, err := json.Marshal(notDirParent)
	if err != nil {
		t.Fatal(err)
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: verify: test@foo.com/mydir/myfile.txt: parent of path is not a directory"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  parentPathName,
		Data: dirParentJSON,
	}

	ds := newDirServer(egcp)
	ds.putHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPut(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The DirEntry of the parent, converted to JSON.
	dirParentJSON, err := json.Marshal(dirParent)
	if err != nil {
		t.Fatal(err)
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  "test@foo.com/mydir",
		Data: dirParentJSON,
	}

	ds := newDirServer(egcp)
	ds.putHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGet(t *testing.T) {
	dirEntryJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  "test@foo.com/mydir/myfile.txt",
		Data: dirEntryJSON,
	}

	resp := nettest.NewExpectingResponseWriter(string(dirEntryJSON))
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/get?pathname="+pathName, dirEntryJSON)

	ds := newDirServer(egcp)
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func newDummyDirServer() *dirServer {
	return newDirServer(&gcptest.DummyGCP{})
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
