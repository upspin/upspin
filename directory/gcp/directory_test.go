package directory

import (
	"encoding/json"
	"errors"
	"log"
	"testing"

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
		Packing: upspin.HTTP,
	}
	location = upspin.Location{
		Reference: reference,
	}
	dirEntry = upspin.DirEntry{
		Name: pathName,
		Metadata: upspin.Metadata{
			IsDir:     false,
			Sequence:  17,
			Signature: []byte("This is a sig!"),
			WrappedKeys: []upspin.WrappedKey{
				upspin.WrappedKey{
					Hash:      [2]byte{1, 3},
					Encrypted: []byte("cipher"),
				},
			}},
	}
)

func TestMkdirError(t *testing.T) {
	d := makeErroringDirectoryClient()

	_, err := d.MakeDirectory(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errMkdirBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errMkdirBadConnection, err)
	}
}

func TestMkdir(t *testing.T) {
	mock := nettest.NewMockHTTPClient(createMockMkdirResponse(t))

	d := New("http://localhost:8080", nil, mock)

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

func createMockMkdirResponse(t *testing.T) []nettest.MockHTTPResponse {
	return []nettest.MockHTTPResponse{createMockLocationResponse(t)}
}

func createMockLocationResponse(t *testing.T) nettest.MockHTTPResponse {
	loc, err := json.Marshal(location)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	return nettest.NewMockHTTPResponse(200, "application/json", loc)
}

func createMockLookupResponse(t *testing.T) []nettest.MockHTTPResponse {
	dir, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	resp := nettest.NewMockHTTPResponse(200, "application/json", dir)
	return []nettest.MockHTTPResponse{resp}
}

func TestLookupError(t *testing.T) {
	d := makeErroringDirectoryClient()

	_, err := d.Lookup(upspin.PathName(pathName))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errLookupBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errLookupBadConnection, err)
	}
}

func TestLookup(t *testing.T) {
	mock := nettest.NewMockHTTPClient(createMockLookupResponse(t))

	d := New("http://localhost:8080", nil, mock)

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
	if string(a.Metadata.Signature) != string(b.Metadata.Signature) {
		log.Println("Signatures differ")
		return false
	}
	if string(a.Metadata.Signature) != string(b.Metadata.Signature) {
		log.Println("Signatures differ")
		return false
	}
	if len(a.Metadata.WrappedKeys) != len(b.Metadata.WrappedKeys) {
		log.Println("WrappedKeys len differ")
		return false
	}
	for i, k := range a.Metadata.WrappedKeys {
		if k.Hash[0] != b.Metadata.WrappedKeys[i].Hash[0] ||
			k.Hash[1] != b.Metadata.WrappedKeys[i].Hash[1] {
			log.Println("Hashes differ")
			return false
		}
		if string(k.Encrypted) != string(b.Metadata.WrappedKeys[i].Encrypted) {
			log.Println("Encrypted keys differ")
			return false
		}
	}
	return true
}

func makeErroringDirectoryClient() *Directory {
	resp := nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	}
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{resp})

	return New("http://localhost:8080", nil, mock)
}

// makeDirectoryClientWithStoreClient creates an upspin.Directory that
// contains a valid upspin.Store which replies successfully to a Put
// request. The dirClientResponse is loaded onto the Directory client
// for testing.
func makeDirectoryClientWithStoreClient(t *testing.T, dirClientResponse nettest.MockHTTPResponse) *Directory {
	// The HTTP client will return a sequence of responses, the first
	// one will be to the Store.Put request, then the second to
	// the Directory.Put request.
	// Setup the mock client
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{createMockLocationResponse(t), dirClientResponse})

	// Get a Store client
	s := store.New("http://localhost:8080", mock)

	// Get a Directory client
	return New("http://localhost:9090", s, mock)
}

func TestPutError(t *testing.T) {
	d := makeDirectoryClientWithStoreClient(t, nettest.MockHTTPResponse{
		Error:    errBadConnection,
		Response: nil,
	})

	_, err := d.Put(upspin.PathName(pathName), []byte("contents"))
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if err.Error() != errPutBadConnection.Error() {
		t.Fatalf("Expected error %v, got %v", errPutBadConnection, err)
	}
}

func TestPut(t *testing.T) {
	respSuccess := nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"error":"Success"}`))

	d := makeDirectoryClientWithStoreClient(t, respSuccess)

	// Issue the put request
	loc, err := d.Put(upspin.PathName("foo@bar.com/mydir/myfile.txt"), []byte("contents of file"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if loc.Reference.Key != key {
		t.Fatalf("Invalid key in location. Expected %v, got %v", key, loc.Reference.Key)
	}
}
