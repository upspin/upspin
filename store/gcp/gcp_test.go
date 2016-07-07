// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/upspin"

	// Import needed storage backend.
	_ "upspin.io/cloud/storage/gcs"
)

const (
	expectedRef   = "978F93921702F861CF941AAACE56B83AE17C8F6845FD674263FFF374A2696A4F"
	serverBaseURL = "http://go-download-from-gcp.goog.com"
	linkForRef    = serverBaseURL + "/ref/978F...4F"
	contents      = "contents of our file"
)

func TestPutAndGet(t *testing.T) {
	s := New(context.New())

	ref, err := s.Put([]byte(contents))
	if err != nil {
		t.Fatal(err)
	}
	if ref != expectedRef {
		t.Errorf("Expected reference %q, got %q", expectedRef, ref)
	}

	data, locs, err := s.Get(ref)
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

func TestDelete(t *testing.T) {
	s := New(context.New())

	err := s.Delete(expectedRef)
	if err != nil {
		t.Fatal(err)
	}
	// TODO(adg): check that the file is deleted
}

// Test some error conditions.

func TestGetInvalidRef(t *testing.T) {
	s := New(context.New())

	_, _, err := s.Get("bla bla bla")
	if err == nil {
		t.Fatal("Expected error")
	}
	expectedError := "Get: not found"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func TestGCPErrorsOut(t *testing.T) {
	s := New(context.New())

	_, _, err := s.Get("123")
	if err == nil {
		t.Fatal("Expected error")
	}
	expectedError := "invalid link returned from GCP"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func TestMissingConfiguration(t *testing.T) {
	store, err := bind.Store(context.New(), upspin.Endpoint{Transport: upspin.GCP})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Get("bla bla bla")
	if err == nil {
		t.Fatalf("Expected error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Expected not configured error, got %q", err)
	}
	bind.Release(store)
}

func TestConfigure(t *testing.T) {
	store, err := bind.Store(context.New(), upspin.Endpoint{Transport: upspin.GCP})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Configure("dance=the macarena")
	if err == nil {
		t.Fatalf("Expected error")
	}
	expected := "syntax error"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Expected %q, got %q", expected, err)
	}
	// now configure it correctly
	err = store.Configure("defaultACL=publicRead", "gcpProjectId=some project id", "gcpBucketName=zee bucket")
	if err != nil {
		t.Fatal(err)
	}
	bind.Release(store)
}
