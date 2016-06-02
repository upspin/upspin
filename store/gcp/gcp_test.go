// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"bytes"
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/cloud/gcp/gcptest"
	"upspin.io/store/gcp/cache"
	"upspin.io/upspin"
)

const (
	expectedRef   = "978F93921702F861CF941AAACE56B83AE17C8F6845FD674263FFF374A2696A4F"
	serverBaseURL = "http://go-download-from-gcp.goog.com"
	linkForRef    = serverBaseURL + "/ref/978F...4F"
	contents      = "contents of our file"
	userName      = "dude@foo.com"
)

func TestPutAndGet(t *testing.T) {
	s := newStoreServer()
	defer s.server.Close()

	ref, err := s.server.Put([]byte(contents))
	if err != nil {
		t.Fatal(err)
	}
	if ref != expectedRef {
		t.Errorf("Expected reference %q, got %q", expectedRef, ref)
	}

	<-s.ch // Wait for the server thread to put to GCP safely.

	data, locs, err := s.server.Get(ref)
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
			Transport: upspin.HTTPS,
			NetAddr:   serverBaseURL,
		},
		Reference: linkForRef,
	}
	if locs[0] != expectedLoc {
		t.Errorf("Expected %v, got %v", expectedLoc, locs[0])
	}
}

func TestGetFromLocalCache(t *testing.T) {
	s := newStoreServer()
	defer s.server.Close()

	// Simulate file still being locally on the server. Get the bytes instead of a new location.
	err := s.server.fileCache.Put(expectedRef, bytes.NewReader([]byte(contents)))
	if err != nil {
		t.Fatal(err)
	}

	data, locs, err := s.server.Get(expectedRef)
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
	defer s.server.Close()

	err := s.server.Delete(expectedRef)
	if err != nil {
		t.Fatal(err)
	}
	gotRef := s.server.cloudClient.(*testGCP).deletedRef
	if gotRef != expectedRef {
		t.Errorf("Expected delete call to %q, got %q", gotRef, expectedRef)
	}
}

// Test some error conditions.

func TestGetInvalidRef(t *testing.T) {
	s := newStoreServer()
	defer s.server.Close()

	_, _, err := s.server.Get("bla bla bla")
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
	defer s.server.Close()

	s.server.cloudClient = &gcptest.ExpectGetGCP{
		Ref:  "123",
		Link: "very poorly-formated url",
	}

	_, _, err := s.server.Get("123")
	if err == nil {
		t.Fatal("Expected error")
	}
	expectedError := "invalid link returned from GCP"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func TestMissingConfiguration(t *testing.T) {
	store, err := bind.Store(&upspin.Context{}, upspin.Endpoint{Transport: upspin.GCP})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Get("bla bla bla")
	if err == nil {
		t.Fatalf("Expected error")
	}
	if !strings.HasPrefix(err.Error(), "not configured") {
		t.Fatalf("Expected not configured error, got %q", err)
	}
	store.Close()
}

func TestConfigure(t *testing.T) {
	store, err := bind.Store(&upspin.Context{}, upspin.Endpoint{Transport: upspin.GCP})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Configure("dance=the macarena")
	if err == nil {
		t.Fatalf("Expected error")
	}
	if !strings.HasPrefix(err.Error(), "invalid configuration") {
		t.Errorf("Expected invalid configuration error, got %q", err)
	}
	// now configure it correctly
	err = store.Configure(ConfigProjectID+"=some project id", ConfigBucketName+"=zee bucket", ConfigTemporaryDir+"=")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
}

func newStoreServer() *storeTestServer {
	ch := make(chan bool)
	return &storeTestServer{
		server: &server{
			cloudClient: &testGCP{
				ExpectGetGCP: gcptest.ExpectGetGCP{
					Ref:  expectedRef,
					Link: linkForRef,
				},
				ch: ch,
			},
			fileCache: cache.NewFileCache(""),
		},
		ch: ch,
	}
}

type storeTestServer struct {
	server *server
	ch     chan bool // channel for listening to GCP events.
}

type testGCP struct {
	gcptest.ExpectGetGCP
	ch         chan bool
	deletedRef string
}

// PutLocalFile implements GCP.
func (t *testGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	t.ch <- true // Inform we've been called.
	return "", nil
}

// Delete implements GCP.
func (t *testGCP) Delete(ref string) error {
	t.deletedRef = ref // Capture the ref
	return nil
}
