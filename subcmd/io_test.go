// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package subcmd

import (
	"fmt"
	"os/user"
	"path/filepath"
	"testing"
)

func testingUserLookup(who string) (*user.User, error) {
	switch who {
	case "":
		return &user.User{
			HomeDir: filepath.Join("/usr", "default"),
		}, nil
	case "ann":
		return &user.User{
			HomeDir: filepath.Join("/usr", "ann"),
		}, nil
	}
	return nil, fmt.Errorf("no such user")
}

var tildeTests = []struct{ in, out string }{
	{"", ""},
	{"~", filepath.Join("/usr", "default")},
	{"~/", filepath.Join("/usr", "default")},
	{"~/x", filepath.Join("/usr", "default", "x")},
	{"~ann", filepath.Join("/usr", "ann")},
	{"~ann/", filepath.Join("/usr", "ann")},
	{"~ann/x", filepath.Join("/usr", "ann", "x")},
	{"~xxx", "~xxx"},
	{"~xxx/", "~xxx"},
	{"~xxx/x", filepath.Join("~xxx", "x")},
}

func TestTilde(t *testing.T) {
	userLookup = testingUserLookup
	defer func() {
		userLookup = user.Lookup
	}()
	for _, test := range tildeTests {
		out := Tilde(test.in)
		if out != test.out {
			t.Errorf("Tilde(%q) = %q; expected %q", test.in, out, test.out)
		}
	}
}

func TestHasGlobChar(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`foo*`, true},
		{`fo?`, true},
		{`foo`, false},
		{`f\*oo`, false},
		{`f\[o]o`, false},
		{`f\[o]o`, false},
		{`foo\\`, false},
		{`foo\/a`, false}, // TODO: OK?
	}
	for _, c := range cases {
		got := HasGlobChar(c.in)
		if got != c.want {
			t.Errorf("HasGlobChar(%q) = %t, want %t", c.in, got, c.want)
		}
	}
}
