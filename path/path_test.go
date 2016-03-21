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
		t.Errorf("got %d allocs, want <=1", int64(mallocs))
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

func TestUserAndDomain(t *testing.T) {
	u, d, err := UserAndDomain(upspin.UserName("ldap@domain.com"))
	if err != nil {
		t.Fatal(err)
	}
	ldap := "ldap"
	domain := "domain.com"
	if u != "ldap" {
		t.Errorf("Expected %q, got %q", ldap, u)
	}
	if d != domain {
		t.Errorf("Expected %q, got %q", domain, d)
	}

	// Now an error
	u, d, err = UserAndDomain(upspin.UserName("foo-bar"))
	if err == nil {
		t.Errorf("Expected an error, got none")
	}
}
