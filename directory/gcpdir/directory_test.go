package gcpdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"testing"

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
	ref                          = upspin.Reference("the reference")
	fileContents                 = []byte("contents of file")
	packData                     = append([]byte{byte(upspin.PlainPack)}, []byte("Packed metadata")...)
	readers                      = []upspin.UserName{upspin.UserName("wife@jones.com")}
	now                          = upspin.Now()
	location                     = upspin.Location{
		Reference: ref,
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
			Sequence: 17,
			Size:     uint64(len(fileContents)),
			Time:     now,
			Packdata: packData,
		},
	}
	optsMeta = upspin.Metadata{
		Sequence: 17,
		Time:     now,
		Size:     uint64(len(fileContents)),
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
	mkdirEntry.Location.Reference = ""
	mkdirEntry.Metadata = upspin.Metadata{
		IsDir:    true,
		Time:     42,
		Size:     0,
		Sequence: 0,
		Packdata: nil,
	}
	// Mkdir will first Lookup the parent, then perform the Mkdir itself
	requestLookup := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8080/get?pathname=%s", parentPathName), nil)
	requestMkdir := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", toJSON(t, mkdirEntry))
	mock := nettest.NewMockHTTPClient(append(newMockLookupParentResponse(t), newMockMkdirResponse(t)...),
		[]*http.Request{requestLookup, requestMkdir})

	d := newDirectory("http://localhost:8080", mock, func() upspin.Time {
		return upspin.Time(42)
	})

	loc, err := d.MakeDirectory(upspin.PathName(pathName))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	// GCP servers don't have a Reference for directory entries since they're stored locally.
	expectedLoc := location
	expectedLoc.Reference = ""
	if loc != expectedLoc {
		t.Fatalf("Expected location %v, got %v", expectedLoc, loc)
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

	d := newDirectory("http://localhost:8080", mock, nil)

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
	for i, k := range a.Metadata.Packdata {
		if k != b.Metadata.Packdata[i] {
			log.Println("Packdata differ")
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

	return newDirectory("http://localhost:8080", mock, nil)
}

func newStore(client netutil.HTTPClientInterface) upspin.Store {
	return store.New("http://localhost:8080", client)
}

func TestPutError(t *testing.T) {
	d := newErroringDirectoryClient()
	de := upspin.DirEntry{
		Name: upspin.PathName(pathName),
		Metadata: upspin.Metadata{
			Packdata: []byte("Packed metadata"),
		},
	}
	err := d.Put(&de) // No location defined.
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errPutBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errPutBadConnection, err)
	}
}

func TestPutBadMeta(t *testing.T) {
	mock := nettest.NewMockHTTPClient(nil, nil)
	d := newDirectory("http://localhost:8081", mock, nil)
	de := &upspin.DirEntry{
		Name: upspin.PathName(pathName),
		Metadata: upspin.Metadata{
			Packdata: []byte(""),
		},
	}
	err := d.Put(de) // No Location specified.
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	badPackingError := "Directory: Put: bob@jones.com/mydir/mysubdir: missing packing type in packdata"
	if err.Error() != badPackingError {
		t.Errorf("Expected error %q, got %q", badPackingError, err)
	}
}

func TestPut(t *testing.T) {
	respSuccess := newResp([]byte(`{"error":"success"}`))

	dirEntryJSON := toJSON(t, dirEntry)
	expectedRequest := nettest.NewRequest(t, netutil.Post, "http://localhost:9090/put", dirEntryJSON)

	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{respSuccess}, []*http.Request{expectedRequest})
	d := newDirectory("http://localhost:9090", mock, nil)

	de := &upspin.DirEntry{
		Name:     upspin.PathName(pathName),
		Metadata: optsMeta,
		Location: location,
	}
	de.Metadata.Packdata = packData

	// Issue the put request
	err := d.Put(de)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify we sent to the Directory service the ref we got back from the Store server
	mock.Verify(t)
}

func TestGlobBadPattern(t *testing.T) {
	// No requests are issued
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{}, []*http.Request{})

	d := newDirectory("http://localhost:8080", mock, nil)

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
	const (
		path0 = "a@b.co/dir1/file1.txt"
		path1 = "a@b.co/dir1/file2.txt"
	)
	responses := []nettest.MockHTTPResponse{newResp(toJSON(t, []upspin.DirEntry{
		*newDirEntry(upspin.PathName(path0)),
		*newDirEntry(upspin.PathName(path1)),
	}))}

	expectedRequests := []*http.Request{
		nettest.NewRequest(t, netutil.Get, "http://localhost:9090/glob?pattern=a@b.co/dir1/*.txt", nil),
	}

	mock := nettest.NewMockHTTPClient(responses, expectedRequests)
	d := newDirectory("http://localhost:9090", mock, nil)

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

func TestDelete(t *testing.T) {
	req := nettest.NewRequest(t, netutil.Delete, "http://localhost:8081/dir/"+pathName, nil)
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{newMockSuccessResponse(t)}, []*http.Request{req})

	d := newDirectory("http://localhost:8081", mock, nil)

	err := d.Delete(pathName)
	if err != nil {
		t.Fatal(err)
	}
	mock.Verify(t)
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
