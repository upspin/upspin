package directory

import (
	"encoding/json"
	"errors"
	"log"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	store "upspin.googlesource.com/upspin.git/store/gcp"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	pathName = "bob@jones.com/myroot/mysubdir"
)

var (
	errBadConnection       = errors.New("bad internet connection")
	errMkdirBadConnection  = newError("MakeDirectory", pathName, errBadConnection)
	errLookupBadConnection = newError("Lookup", pathName, errBadConnection)
	errPutBadConnection    = newError("Put", pathName, errBadConnection)
	key                    = "the key"
	reference              = upspin.Reference{
		Key:     key,
		Packing: upspin.PlainPack,
	}
	location = upspin.Location{
		Reference: reference,
	}
	dirEntry = upspin.DirEntry{
		Name:     pathName,
		Location: location,
		Metadata: upspin.Metadata{
			IsDir:    false,
			Sequence: 17,
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
	return nettest.NewMockHTTPResponse(200, "application/json", loc)
}

func newMockKeyResponse(t *testing.T) nettest.MockHTTPResponse {
	keyJSON, err := json.Marshal(&struct{ Key string }{Key: key})
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	return nettest.NewMockHTTPResponse(200, "application/json", keyJSON)
}

func newMockLookupResponse(t *testing.T) []nettest.MockHTTPResponse {
	dir, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", dir)
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
		ServerURL: "http://localhost:8080",
		Client:    client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
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
	loc, err := d.Put(upspin.PathName(pathName), []byte("contents of file"), []byte("Packed metadata"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if loc.Reference.Key != key {
		t.Fatalf("Invalid key in location. Expected %v, got %v", key, loc.Reference.Key)
	}
	// Verify we sent to the Directory service the Reference.Key we got back from the Store server
	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Sent wrong number of requests. Expected 2, got %v", len(reqs))
	}
	// Look at the second request, which is the one that went to the Directory.
	u := reqs[1].URL
	if u.Scheme != "http" {
		t.Fatalf("Expected http request, got %v", u.Scheme)
	}
	if u.Host != "localhost:9090" {
		t.Fatalf("Expected request to localhost:8080, got %v", u.Host)
	}
	if u.Path != "/put" {
		t.Fatalf("Expected request to /put, got %v", u.Path)
	}
	if u.RawQuery != "" {
		t.Fatalf("Expected no query, got %v", u.RawQuery)
	}
	minLen := len(pathName) + len(loc.Reference.Key) + 2 // 2 bytes for IsDir + Sequence.
	if reqs[1].ContentLength < int64(minLen) {
		t.Fatalf("Request body too small. Expect at least %d, got %d", len(pathName), reqs[1].ContentLength)
	}
	// Read the request buffer (since it was never really consumed by the mock) to see if it was created correctly.
	buf := make([]byte, reqs[1].ContentLength)
	n, err := reqs[1].Body.Read(buf)
	if err != nil {
		t.Fatalf("Can't read buf: %v", buf)
	}
	defer reqs[1].Body.Close()
	buf = buf[:n] // Re-slice
	var de upspin.DirEntry
	err = json.Unmarshal(buf, &de)
	if err != nil {
		t.Fatalf("Error unmarshaling: %v", err)
	}
	// Check that dirEntry matches our expectations of what should have been written
	if string(de.Name) != string(dirEntry.Name) {
		t.Errorf("Invalid pathname. Expected %v, got %v", dirEntry.Name, de.Name)
	}
	if string(de.Location.Reference.Key) != string(dirEntry.Location.Reference.Key) {
		t.Errorf("Invalid key. Expected %v, got %v", dirEntry.Location.Reference.Key, de.Location.Reference.Key)
	}
}

func newDirectory(serverURL string, storeService upspin.Store, client HTTPClientInterface) upspin.Directory {
	context := Context{
		ServerURL:    serverURL,
		StoreService: storeService,
		Client:       client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
	}
	dir, err := access.BindDirectory(context, e)
	if err != nil {
		log.Fatalf("Can't BindDirectory: %v", err)
	}
	return dir
}
