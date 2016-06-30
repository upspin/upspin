// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"strings"
	"testing"
)

func TestParseAndString(t *testing.T) {
	assertParsesAndEncodes(t, "gcp,https://localhost:8080")
	assertParsesAndEncodes(t, "remote,https://localhost:8080")
	assertParsesAndEncodes(t, "inprocess")
}

func TestErrorCases(t *testing.T) {
	assertError(t, "remote", "requires a netaddr")
	assertError(t, "supersonic,https://supersonic.com", "unknown transport type")
	assertError(t, "gcp", "requires a netaddr")
}

// Test printing of an erroneous endpoint. Mostly to protect
// against an error found by vet and fixed.
func TestErroneousString(t *testing.T) {
	e := Endpoint{Transport: 127, NetAddr: "whatnot"}
	const expect = "unknown endpoint {127, whatnot}"
	got := e.String()
	if got != expect {
		t.Fatal("expected %q; got %q", expect, got)
	}
}

func assertError(t *testing.T, epString string, substringError string) {
	_, err := ParseEndpoint(epString)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), substringError) {
		t.Errorf("Expected error prefix %q, got %q", substringError, err)
	}
}

func assertParsesAndEncodes(t *testing.T, epString string) {
	ep, err := ParseEndpoint(epString)
	if err != nil {
		t.Fatal(err)
	}
	retStr := ep.String()
	if retStr != epString {
		t.Errorf("Expected %s, got %s", epString, retStr)
	}
}
