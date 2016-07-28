// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"strings"
	"testing"

	"upspin.io/upspin"
)

var testUser = &upspin.User{
	Name: "beavis@bhead.com",
	Dirs: []upspin.Endpoint{
		upspin.Endpoint{Transport: upspin.GCP, NetAddr: "hello.com"},
		upspin.Endpoint{Transport: upspin.HTTPS, NetAddr: "https.com"},
	},
	Stores: []upspin.Endpoint{
		upspin.Endpoint{Transport: upspin.Unassigned},
	},
	PublicKey: "p256\n123\n456\n",
}

func TestParse(t *testing.T) {
	buf := `{"name":"beavis@bhead.com","dirs":["gcp,hello.com","https,https.com"],"stores":["unassigned"],"publicKey":"p256\n123\n456\n"}`
	user, err := userUnmarshalJSON([]byte(buf))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*testUser, *user) {
		// TODO: pretty print location of error
		t.Errorf("Expected %v, got %v", testUser, user)
	}
}

func TestRoundTrip(t *testing.T) {
	buf, err := userMarshalJSON(testUser)
	if err != nil {
		t.Fatal(err)
	}
	userBack, err := userUnmarshalJSON(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*testUser, *userBack) {
		// TODO: pretty print location of error
		t.Errorf("Expected %v, got %v", testUser, userBack)
	}
}

func TestParseError(t *testing.T) {
	expectError(t,
		`{"name":"beavis@bhead.com","dirs":["gcp","https,https.com"]}`,
		`"gcp" requires a netaddr`)
	expectError(t,
		`{"name":"beavis@bhead.com","stores":[3,"hello.com"]}`,
		"cannot unmarshal number")
	expectError(t,
		`{"name":"beavis@bhead.com","stores":["hello"]}`,
		"unknown transport")
}

func expectError(t *testing.T, buf string, errSubStr string) {
	_, err := userUnmarshalJSON([]byte(buf))
	if err == nil {
		t.Fatalf("Expected error sub string %q, got none", errSubStr)
	}
	if !strings.Contains(err.Error(), errSubStr) {
		t.Errorf("Expected error sub string %q, got %q", errSubStr, err)
	}
}
