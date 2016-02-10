package path

import (
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"
)

func newP(elems []string) Parsed {
	return Parsed{
		User:  "u@google.com",
		Elems: elems,
	}
}

type parseTest struct {
	path     upspin.PathName
	parse    Parsed
	filePath string
}

var goodParseTests = []parseTest{
	{"u@google.com/", newP([]string{}), "/"},
	{"u@google.com/a", newP([]string{"a"}), "/a"},
	{"u@google.com/a/", newP([]string{"a"}), "/a"},
	{"u@google.com/a///b/c/d/", newP([]string{"a", "b", "c", "d"}), "/a/b/c/d"},
	{"u@google.com//a///b/c/d//", newP([]string{"a", "b", "c", "d"}), "/a/b/c/d"},
	// Longer than the backing array in Parsed.
	{"u@google.com/a/b/c/d/e/f/g/h/i/j/k/l/m",
		newP([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"}),
		"/a/b/c/d/e/f/g/h/i/j/k/l/m"},
	// Dot.
	{"u@google.com/.", newP([]string{}), "/"},
	{"u@google.com/a/../b", newP([]string{"b"}), "/b"},
	{"u@google.com/./a///b/./c/d/./.", newP([]string{"a", "b", "c", "d"}), "/a/b/c/d"},
	// Dot-Dot.
	{"u@google.com/..", newP([]string{}), "/"},
	{"u@google.com/a/../b", newP([]string{"b"}), "/b"},
	{"u@google.com/../a///b/../c/d/..", newP([]string{"a", "c"}), "/a/c"},
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
		filePath := pn.FilePath()
		if filePath != test.filePath {
			t.Errorf("%q: DirPath expected %v got %v", test.path, test.filePath, filePath)
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
	if mallocs > 1 {
		t.Errorf("got %v allocs, want <=1", mallocs)
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
