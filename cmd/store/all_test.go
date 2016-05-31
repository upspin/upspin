package main

import (
	"bytes"
	"testing"

	"strings"

	"upspin.io/cloud/gcp/gcptest"
	"upspin.io/cmd/store/cache"
	"upspin.io/upspin"
)

const (
	expectedRef = "978F93921702F861CF941AAACE56B83AE17C8F6845FD674263FFF374A2696A4F"
	linkForRef  = "http://go-download-from-gcp/ref/978F...4F"
	contents    = "contents of our file"
	userName    = "dude@foo.com"
)

var (
	fileCache = cache.NewFileCache("")
	ch        = make(chan bool)
)

func TestPutAndGet(t *testing.T) {
	s := newStoreServer()

	ref, err := s.Put(userName, []byte(contents))
	if err != nil {
		t.Fatal(err)
	}
	if ref != expectedRef {
		t.Errorf("Expected reference %q, got %q", expectedRef, ref)
	}

	<-ch // Wait for the server thread to put to GCP safely.

	data, locs, err := s.Get(userName, ref)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Fatal("Expected data to be nil")
	}
	if len(locs) != 1 {
		t.Fatalf("Expected one new location, got %d", len(locs))
	}
	expectedLoc := upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.GCP,
		},
		Reference: linkForRef,
	}
	if locs[0] != expectedLoc {
		t.Errorf("Expected %v, got %v", expectedLoc, locs[0])
	}
}

func TestGetFromLocalCache(t *testing.T) {
	// File is still locally on the server, get the bytes instead of a new location.
	err := fileCache.Put(expectedRef, bytes.NewReader([]byte(contents)))
	if err != nil {
		t.Fatal(err)
	}

	s := newStoreServer()
	data, locs, err := s.Get(userName, expectedRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 0 {
		t.Fatalf("Expected no new location, got %d", len(locs))
	}
	if data == nil {
		t.Fatal("Expected data")
	}
	if string(data) != contents {
		t.Errorf("Expected contents %q, got %q", contents, data)
	}
}

func TestDelete(t *testing.T) {
	s := newStoreServer()

	err := s.Delete(userName, expectedRef)
	if err != nil {
		t.Fatal(err)
	}
	gotRef := s.cloudClient.(*testGCP).deletedRef
	if gotRef != expectedRef {
		t.Errorf("Expected delete call to %q, got %q", gotRef, expectedRef)
	}
}

// Test some error conditions.

func TestGetInvalidRef(t *testing.T) {
	s := newStoreServer()

	_, _, err := s.Get(userName, "bla bla bla")
	if err == nil {
		t.Fatal("Expected error")
	}
	expectedError := "Get: not found"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func TestGCPErrorsOut(t *testing.T) {
	s := newStoreServer()
	s.cloudClient = &gcptest.ExpectGetGCP{
		Ref:  "123",
		Link: "very poorly-formated url",
	}

	_, _, err := s.Get(userName, "123")
	if err == nil {
		t.Fatal("Expected error")
	}
	expectedError := "invalid link returned from GCP"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func TestMain(m *testing.M) {
	m.Run()
	fileCache.Delete()
}

func newStoreServer() *server {
	return &server{
		cloudClient: &testGCP{
			ExpectGetGCP: gcptest.ExpectGetGCP{
				Ref:  expectedRef,
				Link: linkForRef,
			},
		},
		fileCache: fileCache,
	}
}

type testGCP struct {
	gcptest.ExpectGetGCP
	ch         chan bool
	deletedRef string
}

// PutLocalFile implements GCP.
func (t *testGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	ch <- true // Inform we've been called.
	return "", nil
}

// Delete implements GCP.
func (t *testGCP) Delete(ref string) error {
	t.deletedRef = ref // Capture the ref
	return nil
}
