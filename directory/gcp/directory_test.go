package directory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	store "upspin.googlesource.com/upspin.git/store/gcp"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	pathName    = "bob@jones.com/myroot/mysubdir"
	badPathName = "invalid/path/name"
)

var (
	errBadConnection       = errors.New("bad internet connection")
	errBadPathUserName     = errors.New("bad user name in path")
	errMkdirBadConnection  = newError("MakeDirectory", pathName, errBadConnection)
	errLookupBadConnection = newError("Lookup", pathName, errBadConnection)
	errPutBadConnection    = newError("Put", pathName, errBadConnection)
	errGlobBadPath         = newError("Glob", badPathName, errBadPathUserName)
	key                    = "the key"
	fileContents           = []byte("contents of file")
	reference              = upspin.Reference{
		Key: key,
	}
	location = upspin.Location{
		Reference: reference,
		Endpoint: upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   upspin.NetAddr("http://localhost:8080"),
		},
	}
	dirEntry = upspin.DirEntry{
		Name:     pathName,
		Location: location,
		Metadata: upspin.Metadata{
			IsDir:    false,
			Sequence: 0,
			PackData: []byte("Packed metadata"),
		},
	}
)

func TestMkdirError(t *testing.T) {
	d := newErroringDirectoryClient()

	_, err := d.MakeDirectory(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errMkdirBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errMkdirBadConnection, err)
	}
}

func TestMkdir(t *testing.T) {
	mock := nettest.NewMockHTTPClient(newMockMkdirResponse(t))

	d := newDirectory("http://localhost:8080", nil, mock)

	loc, err := d.MakeDirectory(upspin.PathName(pathName))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if loc != location {
		t.Fatalf("Location differs. Expected %v, got %v", location, loc)
	}
	// Verifies request was sent correctly
	mkdirEntry := dirEntry
	mkdirEntry.Location = upspin.Location{}
	mkdirEntry.Metadata.IsDir = true
	mkdirEntry.Metadata.PackData = nil
	request := newRequest(t, netutil.Post, "http://localhost:8080/put", toJSON(t, mkdirEntry))
	nettest.VerifyRequests(t, []*http.Request{request}, mock.Requests())

	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Sent more requests than necessary. Expected 1, got %v", len(reqs))
	}
	u := reqs[0].URL
	if u.Scheme != "http" {
		t.Fatalf("Expected http request, got %v", u.Scheme)
	}
	if u.Host != "localhost:8080" {
		t.Fatalf("Expected request to localhost:8080, got %v", u.Host)
	}
	if u.Path != "/put" {
		t.Fatalf("Expected request to /put, got %v", u.Path)
	}
	if u.RawQuery != "" {
		t.Fatalf("Expected no query, got %v", u.RawQuery)
	}
	if reqs[0].ContentLength < int64(len(pathName)) {
		t.Fatalf("Request body too small. Expect at least %d, got %d", len(pathName), reqs[0].ContentLength)
	}
}

func newMockMkdirResponse(t *testing.T) []nettest.MockHTTPResponse {
	return []nettest.MockHTTPResponse{newMockLocationResponse(t)}
}

func newMockLocationResponse(t *testing.T) nettest.MockHTTPResponse {
	loc, err := json.Marshal(location)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	return newResp(loc)
}

func newMockKeyResponse(t *testing.T) nettest.MockHTTPResponse {
	keyJSON, err := json.Marshal(&struct{ Key string }{Key: key})
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	return newResp(keyJSON)
}

func newMockLookupResponse(t *testing.T) []nettest.MockHTTPResponse {
	dir, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := newResp(dir)
	return []nettest.MockHTTPResponse{resp}
}

func TestLookupError(t *testing.T) {
	d := newErroringDirectoryClient()

	_, err := d.Lookup(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errLookupBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errLookupBadConnection, err)
	}
}

func TestLookup(t *testing.T) {
	mock := nettest.NewMockHTTPClient(newMockLookupResponse(t))

	d := newDirectory("http://localhost:8080", nil, mock)

	dir, err := d.Lookup(pathName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if dir == nil {
		t.Fatal("Got a nil dirEntry")
	}
	if !dirEntryEquals(&dirEntry, dir) {
		t.Fatalf("Invalid dirEntry. Expected %v, got %v", dirEntry, dir)
	}
}

func dirEntryEquals(a, b *upspin.DirEntry) bool {
	if string(a.Name) != string(b.Name) {
		log.Println("Pathnames differ")
		return false
	}
	if a.Metadata.IsDir != b.Metadata.IsDir {
		log.Println("IsDir differ")
		return false
	}
	if a.Metadata.Sequence != b.Metadata.Sequence {
		log.Println("Sequences differ")
		return false
	}
	for i, k := range a.Metadata.PackData {
		if k != b.Metadata.PackData[i] {
			log.Println("PackData differ")
			return false
		}
	}
	return true
}

func newErroringDirectoryClient() upspin.Directory {
	resp := nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})

	return newDirectory("http://localhost:8080", nil, mock)
}

func newStore(client store.HTTPClientInterface) upspin.Store {
	context := store.Context{
		Client: client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr("http://localhost:8080"),
	}
	s, err := access.BindStore(context, e)
	if err != nil {
		log.Fatalf("Can't bind: %v", err)
	}
	return s
}

// newDirectoryClientWithStoreClient creates an upspin.Directory that
// contains a valid upspin.Store which replies successfully to a Put
// request. The dirClientResponse is loaded onto the Directory client
// for testing. Returns the Directory as well as the mock client for
// post-request inspections.
func newDirectoryClientWithStoreClient(t *testing.T, dirClientResponse nettest.MockHTTPResponse) (upspin.Directory, *nettest.MockHTTPClient) {
	// The HTTP client will return a sequence of responses, the
	// first one will be to the Store.Put request, then the second
	// to the Directory.Put request.  Setup the mock client
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{newMockKeyResponse(t), dirClientResponse})

	// Get a Store client
	s := newStore(mock)

	// Get a Directory client
	return newDirectory("http://localhost:9090", s, mock), mock
}

func TestPutError(t *testing.T) {
	d, _ := newDirectoryClientWithStoreClient(t, nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	})

	_, err := d.Put(upspin.PathName(pathName), []byte("contents"), []byte("Packed metadata"))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errPutBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errPutBadConnection, err)
	}
}

func TestPut(t *testing.T) {
	respSuccess := nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"error":"Success"}`))

	d, mock := newDirectoryClientWithStoreClient(t, respSuccess)

	// Issue the put request
	loc, err := d.Put(upspin.PathName(pathName), fileContents, []byte("Packed metadata"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if loc.Reference.Key != key {
		t.Fatalf("Invalid key in location. Expected %v, got %v", key, loc.Reference.Key)
	}
	// Verify we sent to the Directory service the Reference.Key we got back from the Store server
	dirEntryJSON := toJSON(t, dirEntry)
	expectedRequest := newRequest(t, netutil.Post, "http://localhost:9090/put", dirEntryJSON)

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Sent wrong number of requests. Expected 2, got %v", len(reqs))
	}
	// Look at the second request only which is the one that went to
	// the Directory. Store has been tested already.
	nettest.VerifyRequests(t, []*http.Request{expectedRequest}, reqs[1:])
}

func TestGlobBadPath(t *testing.T) {
	resp := nettest.MockHTTPResponse{
		Error:    errGlobBadPath,
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})

	d := newDirectory("http://localhost:8080", nil, mock)

	_, err := d.Glob(badPathName)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if err.Error() != errGlobBadPath.Error() {
		t.Fatalf("Invalid error. Expected %v, got %v", errGlobBadPath, err)
	}
}

func TestGlob(t *testing.T) {
	// Setup all the responses from the server:
	// First, the server will give us 3 paths from a /list request.
	// Then it will two DirEntry due to two Lookup requests.
	// We later check that we issued one /list request and two Lookup requests.

	const (
		path0 = "a@b.co/dir1/file1.txt"
		path1 = "a@b.co/dir1/file2.txt"
	)
	responses := []nettest.MockHTTPResponse{
		newResp([]byte(fmt.Sprintf(`{ "Names": ["%v","%v","a@b.co/dir1/file3.pdf"]}`, path0, path1))),
		newResp(toJSON(t, newDirEntry(upspin.PathName(path0)))),
		newResp(toJSON(t, newDirEntry(upspin.PathName(path1)))),
	}

	expectedRequests := []*http.Request{
		newRequest(t, netutil.Get, "http://localhost:9090/list?prefix=a@b.co/dir1", nil),
		newRequest(t, netutil.Get, fmt.Sprintf("http://localhost:9090/get?pathname=%v", path0), nil),
		newRequest(t, netutil.Get, fmt.Sprintf("http://localhost:9090/get?pathname=%v", path1), nil),
	}

	mock := nettest.NewMockHTTPClient(responses)
	d := newDirectory("http://localhost:9090", nil, mock)

	dirEntries, err := d.Glob("a@b.co/dir1/*.txt")
	if err != nil {
		t.Fatalf("Unexpected error occurred: %v", err)
	}
	if len(dirEntries) != 2 {
		t.Fatalf("Invalid number of results. Expected 2, got %v", len(dirEntries))
	}
	if string(dirEntries[0].Name) != path0 {
		t.Errorf("Invalid dirEntries[0]. Expected %v, got %v", path0, dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != path1 {
		t.Errorf("Invalid dirEntries[1]. Expected %v, got %v", path1, dirEntries[1].Name)
	}
	nettest.VerifyRequests(t, expectedRequests, mock.Requests())
}

// newDirEntry creates a new DirEntry with the given path name
func newDirEntry(pathName upspin.PathName) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name: pathName,
	}
}

// toJSON is a convenience function for marshaling data into JSON
func toJSON(t *testing.T, data interface{}) []byte {
	d, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Can't marshal to JSON: %v", err)
	}
	return d
}

// newRequest is a convenience function to create an HTTP request of a given type with a given payload.
func newRequest(t *testing.T, reqType, request string, payload []byte) *http.Request {
	var b io.Reader
	if payload != nil {
		b = bytes.NewBuffer(payload)
	}
	r, err := http.NewRequest(reqType, request, b)
	if err != nil {
		t.Fatalf("Error creating a request: %v", err)
	}
	return r
}

// newResp is a convenience function that creates a successful MockHTTPResponse with JSON data.
func newResp(data []byte) nettest.MockHTTPResponse {
	return nettest.NewMockHTTPResponse(200, "application/json", data)
}

func newDirectory(serverURL string, storeService upspin.Store, client HTTPClientInterface) upspin.Directory {
	context := Context{
		StoreService: storeService,
		Client:       client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(serverURL),
	}
	dir, err := access.BindDirectory(context, e)
	if err != nil {
		log.Fatalf("Can't BindDirectory: %v", err)
	}
	return dir
}
