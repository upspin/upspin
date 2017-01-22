// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAndString(t *testing.T) {
	tests := []string{
		"remote,localhost:8080",
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
	e := Endpoint{Transport: Remote, NetAddr: "whatnot"}
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

// TestYAML tests the marshaling/unmarshaling an Endpoint as a YAML string.
func TestYAML(t *testing.T) {
	e := Endpoint{Transport: Remote, NetAddr: "host.example.com"}
	v, err := e.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("unmarshaled into %T, want string", v)
	}
	unmarshal := func(dst interface{}) error {
		d, ok := dst.(*string)
		if !ok {
			return fmt.Errorf("unmarshal passed %T, want *string", dst)
		}
		*d = s
		return nil
	}
	e2 := Endpoint{}
	if err := e2.UnmarshalYAML(unmarshal); err != nil {
		t.Fatal(err)
	}
	if e2 != e {
		t.Fatalf("got %v, want %v", e2, e)
	}
}
