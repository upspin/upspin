package path

import (
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"
)

type parseTest struct {
	path    upspin.PathName
	parse   Parsed
	dirPath string
}

var goodParseTests = []parseTest{
	{"u@google.com/", Parsed{"u@google.com", []string{}}, "/"},
	{"u@google.com/a", Parsed{"u@google.com", []string{"a"}}, "/a"},
	{"u@google.com/a/", Parsed{"u@google.com", []string{"a"}}, "/a"},
	{"u@google.com/a///b/c/d/", Parsed{"u@google.com", []string{"a", "b", "c", "d"}}, "/a/b/c/d"},
	{"u@google.com//a///b/c/d//", Parsed{"u@google.com", []string{"a", "b", "c", "d"}}, "/a/b/c/d"},
}

func TestParse(t *testing.T) {
	for _, test := range goodParseTests {
		pn, err := Parse(test.path)
		if err != nil {
			t.Errorf("%q: unexpected error %v", test.path, err)
			continue
		}
		if !pn.Equal(test.parse) {
			t.Errorf("%q: expected %v got %v", test.path, test.parse, pn)
			continue
		}
		dirPath := pn.DirPath()
		if dirPath != test.dirPath {
			t.Errorf("%q: DirPath expected %v got %v", test.path, test.dirPath, dirPath)
		}
	}
}

func (p Parsed) Equal(q Parsed) bool {
	if p.User != q.User {
		return false
	}
	if len(p.Elems) != len(q.Elems) {
		return false
	}
	for i := range p.Elems {
		if p.Elems[i] != q.Elems[i] {
			return false
		}
	}
	return true
}

var badParseTests = []upspin.PathName{
	"u@google.com", // No slash.
	"u@x/a/b",      // User name too short.
	"user/a/b",     // Invalid user name.
}

func TestBadParse(t *testing.T) {
	for _, test := range badParseTests {
		_, err := Parse(test)
		if err == nil {
			t.Errorf("%q: error, got none", test)
			continue
		}
	}
}
