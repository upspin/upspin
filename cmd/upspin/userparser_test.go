// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"upspin.io/upspin"
)

func TestMissingOrInvalidUserName(t *testing.T) {
	_, err := parseUser([]byte("dirs {"))
	expectError(t, "UserAndDomain", err)
	_, err = parseUser([]byte("foo@"))
	expectError(t, "UserAndDomain", err)
}

func TestBadKey(t *testing.T) {
	_, err := parseUser([]byte("djtrump@disco.com\nkey {\np256\nsomething\nsomething\n"))
	expectError(t, "ParsePublicKey:", err)
}

func TestBadTransport(t *testing.T) {
	_, err := parseUser([]byte("bad@boy.com\ndirs {\n   MegaRemote,upspin.io:443\n}"))
	expectError(t, "unknown transport", err)
}

func TestBadFormat(t *testing.T) {
	_, err := parseUser([]byte("bad@boy.com\nroots {\n}"))
	expectError(t, `syntax error: near "roots {"`, err)
}

func TestValidUserRecord(t *testing.T) {
	u, err := parseUser([]byte(`
                                       edpin-test-2@google.com

dirs {
	remote,localhost:5581
		remote,dir.upspin.io:443
	}
                            stores {
		}
    key {
		p256
		      111123253411110597833011308810463564454002889572718227517999526379120717949960
	94130842991997544147873639047026350007301193739205202959968830407301589935535
}

	`))
	if err != nil {
		t.Fatalf("Expected no error, got %s", err)
	}
	// Verify user record:
	exp := "edpin-test-2@google.com"
	if u.Name != upspin.UserName(exp) {
		t.Errorf("Expected %q, got %q", exp, u.Name)
	}
	if len(u.Dirs) != 2 {
		t.Fatalf("Expected 2 dirs, got %d", len(u.Dirs))
	}
	exp = "remote,localhost:5581"
	if u.Dirs[0].String() != exp {
		t.Errorf("Expected %q, got %q", exp, u.Dirs[0])
	}
	exp = "remote,dir.upspin.io:443"
	if u.Dirs[1].String() != exp {
		t.Errorf("Expected %q, got %q", exp, u.Dirs[1])
	}
	if len(u.Stores) != 0 {
		t.Errorf("Expected zero stores, got %d", len(u.Stores))
	}
	exp = "p256\n111"
	if !strings.Contains(string(u.PublicKey), exp) {
		t.Errorf("Expected %q, got %q", exp, u.PublicKey)
	}
}

func expectError(t *testing.T, expectedErrSubStr string, err error) {
	if err == nil {
		t.Fatalf("Expected error %q, got none", expectedErrSubStr)
	}
	if !strings.Contains(err.Error(), expectedErrSubStr) {
		t.Errorf("Expected %q, got %q", expectedErrSubStr, err)
	}
}
