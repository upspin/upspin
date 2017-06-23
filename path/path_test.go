// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package path

import (
	"testing"

	"upspin.io/upspin"
)

type parseTest struct {
	path      upspin.PathName
	cleanPath upspin.PathName
	isRoot    bool
}

var goodParseTests = []parseTest{
	{"u@google.com", "u@google.com/", true},
	{"u@google.com/", "u@google.com/", true},
	{"u@google.com///", "u@google.com/", true},
	{"u@google.com/a", "u@google.com/a", false},
	{"u@google.com/a////", "u@google.com/a", false},
	{"u@google.com/a///b/c/d/", "u@google.com/a/b/c/d", false},
	// Dot.
	{"u@google.com/.", "u@google.com/", true},
	{"u@google.com/a/../b", "u@google.com/b", false},
	{"u@google.com/./a///b/./c/d/./.", "u@google.com/a/b/c/d", false},
	// Dot-Dot.
	{"u@google.com/..", "u@google.com/", true},
	{"u@google.com/a/../b", "u@google.com/b", false},
	{"u@google.com/../a///b/../c/d/..", "u@google.com/a/c", false},
}

func TestParse(t *testing.T) {
	for _, test := range goodParseTests {
		p, err := Parse(test.path)
		if err != nil {
			t.Errorf("%q: unexpected error %v", test.path, err)
			continue
		}
		if p.path != test.cleanPath {
			t.Errorf("%q: expected %v got %v", test.path, test.cleanPath, p.path)
			continue
		}
		if test.isRoot != p.IsRoot() {
			t.Errorf("%q: expected IsRoot %v, got %v", test.path, test.isRoot, p.IsRoot())
		}
	}
}

func TestCountMallocs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping malloc count in short mode")
	}
	parse := func() {
		Parse("u@google.com/a/b/c/d/e/f/g")
	}
	mallocs := testing.AllocsPerRun(100, parse)
	if mallocs != 0 {
		t.Errorf("got %d allocs, want 0", int64(mallocs))
	}
}

var badParseTests = []upspin.PathName{
	"u@x/a/b",              // User name too short.
	"user/a/b",             // Invalid user name.
	"user@domain.com*",     // Spurious character in user name.
	"user@domain.com*/a/b", // Spurious character in user name.
}

func TestBadParse(t *testing.T) {
	for _, test := range badParseTests {
		_, err := Parse(test)
		if err == nil {
			t.Errorf("%q: got no error, expected one", test)
			continue
		}
	}
}

var userTests = []upspin.PathName{
	"a@b.co/",
	"a@b.co/a",
	"a@b.co/a/b/c",
	"a@b.co/a/b/c/d",
}

func TestUser(t *testing.T) {
	const want = "a@b.co"
	for _, test := range userTests {
		p, err := Parse(test)
		if err != nil {
			t.Errorf("error parsing %q: %v\n", test, err)
		}
		user := p.User()
		if user != want {
			t.Errorf("User(%q)=%q; expected %q", test, user, want)
		}
	}
}

type nelemTest struct {
	path  upspin.PathName
	nelem int
}

var nelemTests = []nelemTest{
	{"a@b.co", 0},
	{"a@b.co/", 0},
	{"a@b.co/a", 1},
	{"a@b.co/a/b", 2},
}

func TestNelem(t *testing.T) {
	for _, test := range nelemTests {
		p, err := Parse(test.path)
		if err != nil {
			t.Errorf("error parsing %q: %v\n", test, err)
		}
		nelem := p.NElem()
		if nelem != test.nelem {
			t.Errorf("NElem(%q)=%d; expected %d", test, nelem, test.nelem)
		}
	}
}

type pathTestWithCount struct {
	path   upspin.PathName
	count  int
	expect upspin.PathName
}

var elemTests = []pathTestWithCount{
	{"a@b.co/a/b/c", 0, "a"},
	{"a@b.co/a/b/c", 1, "b"},
	{"a@b.co/a/b/c", 2, "c"},
	{"a@b.co/a", 0, "a"},
}

func TestElem(t *testing.T) {
	for _, test := range elemTests {
		p, err := Parse(test.path)
		if err != nil {
			t.Errorf("error parsing %q: %v\n", test, err)
		}
		elem := p.Elem(test.count)
		if elem != string(test.expect) {
			t.Errorf("Elem(%q, %d)=%q; expected %q", test.path, test.count, elem, test.expect)
		}
	}
}

type prefixTest struct {
	root      upspin.PathName
	path      upspin.PathName
	hasPrefix bool
}

var prefixTests = []prefixTest{
	{"u@google.com/", "u@google.com/", true},
	{"u@google.com/a", "u@google.com/a", true},
	{"u@google.com/a", "u@google.com/a/b", true},
	{"user@domain.com/", "user@domain.com/dir/fileB.txt", true},
	{"u@google.com/a/b/c", "u@google.com/a/b/c/d", true},
	{"u@google.com/", "x@google.com/", false},
	{"u@google.com/a", "u@google.com/b", false},
	{"u@google.com/a/b", "u@google.com/a", false},
}

func TestHasPrefix(t *testing.T) {
	for _, test := range prefixTests {
		p, err := Parse(test.path)
		if err != nil {
			t.Errorf("%q: unexpected error %v", test.path, err)
			continue
		}
		r, err := Parse(test.root)
		if err != nil {
			t.Errorf("%q: unexpected error %v", test.root, err)
			continue
		}
		// Validate the input - they must be canonical.
		if p.Path() != test.path {
			t.Errorf("path %q not canonical", test.path)
			continue
		}
		if r.Path() != test.root {
			t.Errorf("root %q not canonical", test.root)
			continue
		}
		if hasPrefix := p.HasPrefix(r); hasPrefix != test.hasPrefix {
			t.Errorf("HasPrefix(%q, %q)=%t; expected %t", test.path, test.root, hasPrefix, test.hasPrefix)
		}
	}
}

// The join and clean tests are based on those in Go's path/path_test.go.
type JoinTest struct {
	elem []string
	path upspin.PathName
}

var jointests = []JoinTest{
	// zero parameters
	{[]string{}, ""},

	// one parameter
	{[]string{""}, ""},
	{[]string{"a"}, "a"},

	// two parameters
	{[]string{"a", "b"}, "a/b"},
	{[]string{"a", ""}, "a"},
	{[]string{"", "b"}, "b"},
	{[]string{"/", "a"}, "/a"},
	{[]string{"/", ""}, "/"},
	{[]string{"a/", "b"}, "a/b"},
	{[]string{"a/", ""}, "a"},
	{[]string{"", ""}, ""},
}

// join takes a []string and passes it to Join.
func join(args ...string) upspin.PathName {
	if len(args) == 0 {
		return Join("")
	}
	return Join(upspin.PathName(args[0]), args[1:]...)
}

func TestJoin(t *testing.T) {
	for _, test := range jointests {
		if p := join(test.elem...); p != test.path {
			t.Errorf("join(%q) = %q, want %q", test.elem, p, test.path)
		}
	}
}

type pathTest struct {
	path, result upspin.PathName
}

var cleantests = []pathTest{
	// Already clean
	{"", "."},
	{"abc", "abc"},
	{"abc/def", "abc/def"},
	{"a/b/c", "a/b/c"},
	{".", "."},
	{"..", ".."},
	{"../..", "../.."},
	{"../../abc", "../../abc"},
	{"/abc", "/abc"},
	{"/", "/"},

	// Remove trailing slash
	{"abc/", "abc"},
	{"abc/def/", "abc/def"},
	{"a/b/c/", "a/b/c"},
	{"./", "."},
	{"../", ".."},
	{"../../", "../.."},
	{"/abc/", "/abc"},

	// Remove doubled slash
	{"abc//def//ghi", "abc/def/ghi"},
	{"//abc", "/abc"},
	{"///abc", "/abc"},
	{"//abc//", "/abc"},
	{"abc//", "abc"},

	// Remove . elements
	{"abc/./def", "abc/def"},
	{"/./abc/def", "/abc/def"},
	{"abc/.", "abc"},

	// Remove .. elements
	{"abc/def/ghi/../jkl", "abc/def/jkl"},
	{"abc/def/../ghi/../jkl", "abc/jkl"},
	{"abc/def/..", "abc"},
	{"abc/def/../..", "."},
	{"/abc/def/../..", "/"},
	{"abc/def/../../..", ".."},
	{"/abc/def/../../..", "/"},
	{"abc/def/../../../ghi/jkl/../../../mno", "../../mno"},

	// Combinations
	{"abc/./../def", "def"},
	{"abc//./../def", "def"},
	{"abc/../../././../def", "../../def"},

	// Now some real Upspin paths.
	{"joe@blow.com", "joe@blow.com/"}, // User root always has a trailing slash.
	{"joe@blow.com/", "joe@blow.com/"},
	{"joe@blow.com/..", "joe@blow.com/"},
	{"joe@blow.com/../", "joe@blow.com/"},
	{"joe@blow.com/a/b/../b/c", "joe@blow.com/a/b/c"},
}

func TestClean(t *testing.T) {
	for _, test := range cleantests {
		if s := Clean(test.path); s != test.result {
			t.Errorf("Clean(%q) = %q, want %q", test.path, s, test.result)
		}
		if s := Clean(test.result); s != test.result {
			t.Errorf("Clean(%q) = %q, want %q", test.result, s, test.result)
		}
	}
}

type compareTest struct {
	path1, path2 upspin.PathName
	expect       int
}

var compareTests = []compareTest{
	// Some the same
	{"joe@bar.com", "joe@bar.com", 0},
	{"joe@bar.com/", "joe@bar.com", 0},
	{"joe@bar.com/", "joe@bar.com/", 0},
	{"joe@bar.com/a/b/c", "joe@bar.com/a/b/c", 0},
	// Same domain sorts by user.
	{"joe@bar.com", "adam@bar.com", 1},
	{"joe@bar.com/a/b/c", "adam@bar.com/a/b/c", 1},
	{"adam@bar.com", "joe@bar.com", -1},
	{"adam@bar.com/a/b/c", "joe@bar.com/a/b/c", -1},
	// Different paths.
	{"joe@bar.com/a/b/c", "joe@bar.com/a/b/d", -1},
	{"joe@bar.com/a/b/d", "joe@bar.com/a/b/c", 1},
	// Different length paths.
	{"joe@bar.com/a/b/c", "joe@bar.com/a/b/c/d", -1},
	{"joe@bar.com/a/b/c/d", "joe@bar.com/a/b/c", 1},
}

func TestCompare(t *testing.T) {
	for _, test := range compareTests {
		p1, err := Parse(test.path1)
		if err != nil {
			t.Fatalf("%s: %s\n", test.path1, err)
		}
		p2, err := Parse(test.path2)
		if err != nil {
			t.Fatalf("%s: %s\n", test.path2, err)
		}
		if got := p1.Compare(p2); got != test.expect {
			t.Errorf("Compare(%q, %q) = %d; expected %d", test.path1, test.path2, got, test.expect)
		}
		// Verify for compare too.
		if (p1.Compare(p2) == 0) != p1.Equal(p2) {
			t.Errorf("Equal(%q, %q) = %t; expected otherwise", test.path1, test.path2, p1.Equal(p2))
		}
	}
}

var dropPathTests = []pathTestWithCount{
	{"a@b.co/a/b", 1, "a@b.co/a"},
	{"a@b.co/a/b", 2, "a@b.co/"},
	// Won't go past the root.
	{"a@b.co/a/b", 3, "a@b.co/"},
	// Multiple slashes are OK.
	{"a@b.co/a/b///", 1, "a@b.co/a"},
	{"a@b.co///a//////b///c/////", 2, "a@b.co/a"},
	// No slash, but return with one.
	{"a@b.co", 1, "a@b.co/"},
	{"a@b.co", 100, "a@b.co/"},
}

func TestDropPath(t *testing.T) {
	for _, test := range dropPathTests {
		got := DropPath(test.path, test.count)
		if got != test.expect {
			t.Errorf("DropPath(%q, %d) = %q; expected %q", test.path, test.count, got, test.expect)
		}
	}
}

var firstPathTests = []pathTestWithCount{
	{"a@b.co/a/b/c/d", 0, "a@b.co/"},
	{"a@b.co/a/b/c/d", 1, "a@b.co/a"},
	{"a@b.co/a/b/c/d", 2, "a@b.co/a/b"},
	{"a@b.co/a/b/c/d/", 3, "a@b.co/a/b/c"},
	{"a@b.co/a/b/c/d/", 4, "a@b.co/a/b/c/d"},
	{"a@b.co/a/b/c/d", 10, "a@b.co/a/b/c/d"},
	{"a@b.co/a/b/c/d/", 10, "a@b.co/a/b/c/d"},
	// Multiple slashes are OK.
	{"a@b.co/a/b///", 1, "a@b.co/a"},
	{"a@b.co///a//////b///c/////", 2, "a@b.co/a/b"},
	// No slash, but return with one.
	{"a@b.co", 1, "a@b.co/"},
	{"a@b.co", 100, "a@b.co/"},
}

func TestFirstPath(t *testing.T) {
	for _, test := range firstPathTests {
		got := FirstPath(test.path, test.count)
		if got != test.expect {
			t.Errorf("FirstPath(%q, %d) = %q; expected %q", test.path, test.count, got, test.expect)
		}
	}
}
