package service

import "testing"

type parseTest struct {
	path  PathName
	parse parsedPath
}

var goodParseTests = []parseTest{
	{"u@google.com/", parsedPath{"u@google.com", []string{}}},
	{"u@google.com/a", parsedPath{"u@google.com", []string{"a"}}},
	{"u@google.com/a/", parsedPath{"u@google.com", []string{"a"}}},
	{"u@google.com/a///b/c/d/", parsedPath{"u@google.com", []string{"a", "b", "c", "d"}}},
}

func TestParse(t *testing.T) {
	for _, test := range goodParseTests {
		pn, err := parse(test.path)
		if err != nil {
			t.Errorf("%q: unexpected error %v", test.path, err)
			continue
		}
		if !pn.Equal(test.parse) {
			t.Errorf("%q: expected %v got %v", test.path, test.parse, pn)
			continue
		}
	}
}

func (p parsedPath) Equal(q parsedPath) bool {
	if p.user != q.user {
		return false
	}
	if len(p.elems) != len(q.elems) {
		return false
	}
	for i := range p.elems {
		if p.elems[i] != q.elems[i] {
			return false
		}
	}
	return true
}

var badParseTests = []PathName{
	"u@google.com", // No slash.
	"u@x/a/b",      // User name too short.
	"user/a/b",     // Invalid user name.
}

func TestBadParse(t *testing.T) {
	for _, test := range badParseTests {
		_, err := parse(test)
		if err == nil {
			t.Errorf("%q: error, got none", test)
			continue
		}
	}
}
