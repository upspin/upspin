// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package subcmd

import (
	"fmt"
	"os/user"
	"path/filepath"
	"testing"

	"upspin.io/config"
	"upspin.io/upspin"
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

type atSignTest struct {
	in  string
	out upspin.PathName
}

var atSignTests = []atSignTest{
	{"", ""},
	{"ann@example.com", "ann@example.com"},
	{"joe@example.com", "joe@example.com"},
	{"@", "ann@example.com/"},
	{"@/", "ann@example.com/"},
	{"@/boo", "ann@example.com/boo"},
	{"@+suffix", "ann+suffix@example.com/"},
	{"@+suffix/", "ann+suffix@example.com/"},
	{"@+suffix/boo", "ann+suffix@example.com/boo"},
}

var atSignTestsWithSuffix = []atSignTest{
	{"@", "ann+suffix@example.com/"},
	{"@/", "ann+suffix@example.com/"},
	{"@/boo", "ann+suffix@example.com/boo"},
	{"@+extra", "ann+suffix+extra@example.com/"},
	{"@+extra/", "ann+suffix+extra@example.com/"},
	{"@+extra/boo", "ann+suffix+extra@example.com/boo"},
}

func testAtSign(t *testing.T, user upspin.UserName, tests []atSignTest) {
	// Hackily build a State sufficient to invoke the method.
	cfg := config.New()
	cfg = config.SetUserName(cfg, user)
	state := NewState("atsign")
	state.Config = cfg
	for _, test := range tests {
		out := state.AtSign(test.in)
		if out != test.out {
			t.Errorf("AtSign(%q) = %q; expected %q", test.in, out, test.out)
		}
	}
}

func TestAtSign(t *testing.T) {
	testAtSign(t, "ann@example.com", atSignTests)
}

func TestAtSignWithSuffix(t *testing.T) {
	testAtSign(t, "ann+suffix@example.com", atSignTestsWithSuffix)
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
