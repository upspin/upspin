// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"strings"
	"testing"
)

func TestParseAndString(t *testing.T) {
	tests := []string{
		"gcp,localhost:8080",
		"remote,localhost:8080",
		"https,https://localhost:8080",
		"inprocess",
	}
	for _, test := range tests {
		ep, err := ParseEndpoint(test)
		if err != nil {
			t.Errorf("parsing %q: %v", test, err)
			continue
		}
		got := ep.String()
		if got != test {
			t.Errorf("got %q, want %q", got, test)
		}
	}
}

func TestErrorCases(t *testing.T) {
	tests := []struct {
		endpoint, error string
	}{
		{"remote", "requires a netaddr"},
		{"supersonic,https://supersonic.com", "unknown transport type"},
		{"gcp", "requires a netaddr"},
		{"https", "requires a netaddr"},
	}
	for _, test := range tests {
		_, err := ParseEndpoint(test.endpoint)
		if err == nil {
			t.Errorf("expected error for %q", test.endpoint)
			continue
		}
		if !strings.Contains(err.Error(), test.error) {
			t.Errorf("got error %q, expected %q", err, test.error)
		}
	}
}

// Test printing of an erroneous endpoint. Mostly to protect
// against an error found by vet and fixed.
func TestErroneousString(t *testing.T) {
	e := Endpoint{Transport: 127, NetAddr: "whatnot"}
	const expect = "unknown transport {transport(127), whatnot}"
	got := e.String()
	if got != expect {
		t.Fatalf("expected %q; got %q", expect, got)
	}
}

func TestJSON(t *testing.T) {
	e := Endpoint{Transport: GCP, NetAddr: "whatnot"}
	buf, err := e.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	newE := new(Endpoint)
	err = newE.UnmarshalJSON(buf)
	if err != nil {
		t.Fatal(err)
	}
	if e != *newE {
		t.Errorf("Expected %q, got %q", e, newE)
	}
}
