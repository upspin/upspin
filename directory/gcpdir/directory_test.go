package gcpdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"testing"

	"strings"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	store "upspin.googlesource.com/upspin.git/store/gcpstore"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	parentPathName = "bob@jones.com/mydir"
	pathName       = parentPathName + "/mysubdir"
	badPathName    = "invalid/path/name"
)

var (
	errBadConnection             = errors.New("bad internet connection")
	errBadPatternUserName        = errors.New("bad user name in path")
	errLookupParentBadConnection = newError("Lookup", parentPathName, errBadConnection)
	errLookupBadConnection       = newError("Lookup", pathName, errBadConnection)
	errPutBadConnection          = newError("Put", pathName, errBadConnection)
	errGlobBadPattern            = newError("Glob", badPathName, errBadPatternUserName)
	key                          = "the key"
	fileContents                 = []byte("contents of file")
	packData                     = append([]byte{byte(upspin.PlainPack)}, []byte("Packed metadata")...)
	readers                      = []upspin.UserName{upspin.UserName("wife@jones.com")}
	reference                    = upspin.Reference{
		Key:     key,
		Packing: upspin.PlainPack,
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
			PackData: packData,
		},
	}
	// A mock HTTP client that does not do anything
	doNothingHTTPClient = nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{}, []*http.Request{})
)

func TestMkdirError(t *testing.T) {
	d := newErroringDirectoryClient()

	_, err := d.MakeDirectory(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	// MakeDirectory calls Lookup in all cases except the root.
	if err.Error() != errLookupParentBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errLookupParentBadConnection, err)
	}
}

func TestMkdir(t *testing.T) {
	mkdirEntry := dirEntry
	mkdirEntry.Location.Reference = upspin.Reference{}
	mkdirEntry.Metadata.IsDir = true
	mkdirEntry.Metadata.PackData = nil
	mkdirEntry.Metadata.Readers = readers
	// Mkdir will first Lookup the parent, then perform the Mkdir itself
	requestLookup := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?pathname=%s", parentPathName), nil)
	requestMkdir := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", toJSON(t, mkdirEntry))
	mock := nettest.NewMockHTTPClient(append(newMockLookupParentResponse(t), newMockMkdirResponse(t)...), []*http.Request{requestLookup, requestMkdir})

	d := new("http://localhost:8080", newStore(doNothingHTTPClient), mock)

	loc, err := d.MakeDirectory(upspin.PathName(pathName))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	// GCP servers don't have a Key for directory entries since they're stored locally.
	location.Reference.Key = ""
	if loc != location {
		t.Fatalf("Expected location %v, got %v", location, loc)
	}
	// Verifies request was sent correctly
	mock.Verify(t)
}

func newMockMkdirResponse(t *testing.T) []nettest.MockHTTPResponse {
	return []nettest.MockHTTPResponse{newMockSuccessResponse(t)}
}

func newMockSuccessResponse(t *testing.T) nettest.MockHTTPResponse {
	success, err := json.Marshal(&struct{ Error string }{Error: "success"})
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	return newResp(success)
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

func newMockLookupParentResponse(t *testing.T) []nettest.MockHTTPResponse {
	// Set up the parent to contain default Readers.
	newDir := dirEntry
	newDir.Name = parentPathName
	newDir.Metadata.Readers = readers
	dir, err := json.Marshal(newDir)
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
	mock := nettest.NewMockHTTPClient(newMockLookupResponse(t), []*http.Request{nettest.AnyRequest})

	d := new("http://localhost:8080", nil, mock)

	dir, err := d.Lookup(pathName)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if dir == nil {
		t.Fatal("Got a nil dirEntry")
	}
	if !dirEntryEquals(&dirEntry, dir) {
		t.Fatalf("Expected dirEntry %v, got %v", dirEntry, dir)
	}
	mock.Verify(t)
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
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp}, []*http.Request{nettest.AnyRequest})

	return new("http://localhost:8080", newStore(doNothingHTTPClient), mock)
}

func newStore(client netutil.HTTPClientInterface) upspin.Store {
	return store.New("http://localhost:8080", client)
}

// newDirectoryClientWithStoreClient creates an upspin.Directory that
// contains a valid upspin.Store which replies successfully to a Put
// request. The dirClientResponse is loaded onto the Directory client
// for testing and we expect a dirClientRequest to trigger it. Returns
// the Directory as well as the mock client for post-request
// inspections.
func newDirectoryClientWithStoreClient(t *testing.T, dirClientResponse nettest.MockHTTPResponse, dirClientRequest *http.Request) (upspin.Directory, *nettest.MockHTTPClient) {
	// The HTTP client will return a sequence of responses, the
	// first one will be to the Directory server, to Lookup the parent path.
	// Then, the actual Store.Put request, followed by he Directory.Put request.
	storeReq := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", []byte("*"))
	parentLookupReq := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:9090/get?pathname=%s", parentPathName), nil)
	parentLookupResp := newMockLookupParentResponse(t)[0]

	mock := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{parentLookupResp, newMockKeyResponse(t), dirClientResponse},
		[]*http.Request{parentLookupReq, storeReq, dirClientRequest})

	// Get a Store client
	s := newStore(mock)

	// Get a Directory client
	return new("http://localhost:9090", s, mock), mock
}

func TestPutError(t *testing.T) {
	d, _ := newDirectoryClientWithStoreClient(t, nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	}, nettest.AnyRequest)

	_, err := d.Put(upspin.PathName(pathName), []byte("contents"), []byte("Packed metadata"))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errPutBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errPutBadConnection, err)
	}
}

func TestPutBadMeta(t *testing.T) {
	mock := nettest.NewMockHTTPClient(nil, nil)
	d := new("http://localhost:8081", nil, mock)

	_, err := d.Put(upspin.PathName(pathName), []byte("contents"), []byte(""))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	badPackingError := "Put bob@jones.com/mydir/mysubdir: missing packing type in packdata"
	if err.Error() != badPackingError {
		t.Errorf("Expected error %q, got %q", badPackingError, err)
	}
}

func TestPut(t *testing.T) {
	respSuccess := newResp([]byte(`{"error":"success"}`))

	de := dirEntry
	de.Metadata.Readers = readers
	dirEntryJSON := toJSON(t, de)
	expectedRequest := nettest.NewRequest(t, netutil.Post, "http://localhost:9090/put", dirEntryJSON)

	d, mock := newDirectoryClientWithStoreClient(t, respSuccess, expectedRequest)

	// Issue the put request
	loc, err := d.Put(upspin.PathName(pathName), fileContents, packData)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if loc.Reference.Key != key {
		t.Fatalf("Expected key %v, got %v", key, loc.Reference.Key)
	}

	// Verify we sent to the Directory service the Reference.Key we got back from the Store server
	mock.Verify(t)
}

func TestGlobBadPattern(t *testing.T) {
	// No requests are issued
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{}, []*http.Request{})

	d := new("http://localhost:8080", nil, mock)

	_, err := d.Glob(badPathName)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if err.Error() != errGlobBadPattern.Error() {
		t.Fatalf("Expected error %q, got %q", errGlobBadPattern, err)
	}
	mock.Verify(t)
}

func TestGlob(t *testing.T) {
	// Set up all the responses from the server:
	// First, the server will give us 3 paths from a /list request.
	// Then it will send two DirEntry due to our two Lookup requests.
	// We later check that we issued one list request and two Lookup requests.

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
		nettest.NewRequest(t, netutil.Get, "http://localhost:9090/list?prefix=a@b.co/dir1", nil),
		nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:9090/get?pathname=%v", path0), nil),
		nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:9090/get?pathname=%v", path1), nil),
	}

	mock := nettest.NewMockHTTPClient(responses, expectedRequests)
	d := new("http://localhost:9090", nil, mock)

	dirEntries, err := d.Glob("a@b.co/dir1/*.txt")
	if err != nil {
		t.Fatalf("Unexpected error occurred: %v", err)
	}
	if len(dirEntries) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(dirEntries))
	}
	if string(dirEntries[0].Name) != path0 {
		t.Errorf("Expected 0th entry %v, got %v", path0, dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != path1 {
		t.Errorf("Expected 1st entry %v, got %v", path1, dirEntries[1].Name)
	}
	mock.Verify(t)
}

func TestAccessErrorInvalidContents(t *testing.T) {
	const (
		access        = parentPathName + "/Access"
		accessControl = "invalidemail.com"
	)

	// Does not perform a lookup since the Access file is invalid.
	mock := nettest.NewMockHTTPClient(newMockLookupParentResponse(t), []*http.Request{nettest.AnyRequest})
	d := new("http://localhost:8080", newStore(doNothingHTTPClient), mock)

	_, err := d.Put(access, []byte(accessControl), []byte{byte(upspin.PlainPack)})
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	expectedError := "Put : bob@jones.com/mydir/Access: 1: unrecognized text: "
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected %s, got %s", expectedError, err)
	}

	mock.Verify(t)
}

func TestAccess(t *testing.T) {
	const (
		accessPath    = parentPathName + "/Access"
		accessControl = "\n r:dalai@lama.org, bill@gatesfoundation.org\n"
		success       = `{"error":"success"}`
	)

	// We expect d.Put will cause the following updates:
	// 1 - Re-write the parent with new Readers to the Directory server
	// 2 - Write the DirEntry for the actual Access file to the Directory server
	// 3 - Write the contents of the Access file, in plain packing to the Store server
	// Note: we ignore groups for now. Only usernames are recorded for now, not full pathnames.

	// Set up Store
	storeReq := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", []byte("*"))
	storeMock := nettest.NewMockHTTPClient(
		[]nettest.MockHTTPResponse{newMockKeyResponse(t)},
		[]*http.Request{storeReq})
	store := newStore(storeMock)

	// Set up Directory
	deParent := dirEntry
	deParent.Name = parentPathName
	deParent.Metadata.Readers = []upspin.UserName{upspin.UserName("dalai@lama.org"), upspin.UserName("bill@gatesfoundation.org")}
	deParentJSON := toJSON(t, deParent)
	updateParentReq := nettest.NewRequest(t, netutil.Post, "http://localhost:8081/put", deParentJSON)

	deAccess := deParent
	deAccess.Name = accessPath
	deAccess.Metadata.PackData = []byte{byte(upspin.PlainPack)} // Access file does not have packdata
	deAccessJSON := toJSON(t, deAccess)
	parentLookupReq := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8081/get?pathname=%s", parentPathName), nil)
	putAccessReq := nettest.NewRequest(t, netutil.Post, "http://localhost:8081/put", deAccessJSON)

	requests := []*http.Request{parentLookupReq, updateParentReq, putAccessReq}
	responses := []nettest.MockHTTPResponse{newMockLookupParentResponse(t)[0], newResp([]byte(success)), newResp([]byte(success))}

	dirMock := nettest.NewMockHTTPClient(responses, requests)
	d := new("http://localhost:8081", store, dirMock)

	_, err := d.Put(accessPath, []byte(accessControl), []byte{byte(upspin.PlainPack)})
	if err != nil {
		t.Fatal(err)
	}

	dirMock.Verify(t)
	storeMock.Verify(t)
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

// newResp is a convenience function that creates a successful MockHTTPResponse with JSON data.
func newResp(data []byte) nettest.MockHTTPResponse {
	return nettest.NewMockHTTPResponse(200, "application/json", data)
}
