package parser

import (
	"encoding/json"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverParsingErrorPrefix = "can't parse reply from server:"
)

var (
	location = upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   "some server",
		},
		Reference: "abcd",
	}
	dirEntry = upspin.DirEntry{
		Name:     "foo",
		Location: location,
	}
)

func TestLocationResponse(t *testing.T) {
	locJSON, err := json.Marshal(location)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := LocationResponse(locJSON)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if loc == nil {
		t.Fatal("Expected a valid location, got nil")
	}
	if loc.Reference != location.Reference {
		t.Errorf("Expected key %v, got %v", location.Reference, loc.Reference)
	}
}

func TestLocationResponseBadError(t *testing.T) {
	loc, err := LocationResponse([]byte(`{"endpoint:"foo", "bla bla bla"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestLocationResponseEmptyError(t *testing.T) {
	loc, err := LocationResponse([]byte(""))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestLocationResponseWithProperError(t *testing.T) {
	loc, err := LocationResponse([]byte(`{"error":"something bad happened"}`))
	if err == nil {
		t.Fatalf("Expected error, got nil: %v", loc)
	}
	if loc != nil {
		t.Fatalf("Expected a zero Location, got %v", *loc)
	}
	expectedError := "something bad happened"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestRefResponse(t *testing.T) {
	ref, err := ReferenceResponse([]byte(`{"ref": "1234"}`))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if ref != "1234" {
		t.Errorf("Expected key 1234, got %v", ref)
	}
}

func TestRefResponseBadError(t *testing.T) {
	ref, err := ReferenceResponse([]byte("bla bla bla"))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if ref != "" {
		t.Fatalf("Expected a nil key, got %v", ref)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponse(t *testing.T) {
	dirJSON, err := json.Marshal(dirEntry)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := DirEntryResponse(dirJSON)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if dir == nil {
		t.Fatal("Expected a valid dirEntry, got nil")
	}
	if dir.Location.Reference != dirEntry.Location.Reference {
		t.Errorf("Expected key %v, got %v", dirEntry.Location.Reference, dir.Location.Reference)
	}
}

func TestDirEntryResponseBadError(t *testing.T) {
	dir, err := DirEntryResponse([]byte(`{"Name":"path","Location":"loc"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if dir != nil {
		t.Fatalf("Expected a nil DirEntry, got %v", *dir)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponseNilDirEntry(t *testing.T) {
	dir, err := DirEntryResponse(nil)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestDirEntryResponseEmptyDirEntry(t *testing.T) {
	dir, err := DirEntryResponse([]byte(""))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	expectedError := "empty server response"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}

func TestDirEntryResponseInvalidDirEntry(t *testing.T) {
	dir, err := DirEntryResponse([]byte("bla"))
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if dir != nil {
		t.Errorf("Expected nil dir, got %v", *dir)
	}
	if !strings.HasPrefix(err.Error(), serverParsingErrorPrefix) {
		t.Fatalf("Expected error prefix %q, got %q", serverParsingErrorPrefix, err)
	}
}

func TestDirEntryResponseWithProperError(t *testing.T) {
	dir, err := DirEntryResponse([]byte(`{"error":"something terrible happened"}`))
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if dir != nil {
		t.Fatalf("Expected a nil DirEntry, got %v", *dir)
	}
	expectedError := "something terrible happened"
	if err.Error() != expectedError {
		t.Fatalf("Expected error %q, got %q", expectedError, err)
	}
}
