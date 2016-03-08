package access_test

import (
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

func TestParse(t *testing.T) {
	accessText := `
  r : foo@bob.com ,a@b.co, x@y.uk // a comment

w:writerjoe@a.bc # comment r: ignored@incomment.com
a: m@n.mn  // other comment a: ignored@too.com
r:extra@reader.org`

	p, err := access.Parse(accessText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{"foo@bob.com", "a@b.co", "x@y.uk", "extra@reader.org"})
	containsAll(t, p.Writers, []string{"writerjoe@a.bc"})
	containsAll(t, p.Appenders, []string{"m@n.mn"})
}

func TestParseEmptyFile(t *testing.T) {
	accessText := "\n # Just a comment.\n // Nothing to see here \n \n \n\t\n"
	p, err := access.Parse(accessText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestParseContainsGroupName(t *testing.T) {
	accessText := "r: family,*@google.com,edpin@google.com/Groups/friends"
	p, err := access.Parse(accessText)
	if err != nil {
		t.Fatal(err)
	}
	// Group names such as "family" are currently ignored.
	// TODO: implement groups.
	containsAll(t, p.Readers, []string{"*@google.com", "edpin@google.com/Groups/friends"})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestParseWrongFormat1(t *testing.T) {
	const (
		expectedErr = "unrecognized text in Access file"
	)
	accessText := "read: bob@abc.com" // "read" is wrong. should be "r"
	_, err := access.Parse(accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.HasPrefix(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat2(t *testing.T) {
	const (
		expectedErr = "invalid format on line 2"
	)
	accessText := "//A comment\n #Another comment\n r: a@b.co : x"
	_, err := access.Parse(accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.HasPrefix(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseTooManyFieldsOnSingleLine(t *testing.T) {
	const (
		expectedErr = "invalid format on line 0"
	)
	accessText := "r: a@b.co r: c@b.co"
	_, err := access.Parse(accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.HasPrefix(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseBadPath(t *testing.T) {
	const (
		expectedErr = "invalid format on line 2"
	)
	// TODO: Group names are being ignored. When implemented, this group name should cause an error.
	accessText := "r: notanemail/Group/family"
	p, err := access.Parse(accessText)
	if err != nil {
		t.Fatal(err)
	}
	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestIsAccessFile(t *testing.T) {
	var nonNil path.Parsed
	expectState(t, true, &nonNil, upspin.PathName("a@b.com/Access"))
	expectState(t, true, &nonNil, upspin.PathName("a@b.com/dir/subdir/Access"))
	expectState(t, false, &nonNil, upspin.PathName("a@b.com/dir/subdir/access"))
	expectState(t, true, &nonNil, upspin.PathName("a@b.com/dir/subdir/Access/")) // weird, but maybe ok?
	expectState(t, false, nil, upspin.PathName("booboo/dir/subdir/Access"))
	expectState(t, false, nil, upspin.PathName("not a path"))
}

func containsAll(t *testing.T, p []path.Parsed, expect []string) {
	if len(p) != len(expect) {
		t.Fatalf("Expected %d paths, got %d", len(expect), len(p))
	}
	for _, path := range p {
		var compare string
		if len(path.Elems) == 0 {
			compare = string(path.User)
		} else {
			compare = path.String()
		}
		if !found(expect, compare) {
			t.Fatalf("User not found in list: %s", compare)
		}
	}
}

func found(haystack []string, needle string) bool {
	log.Printf("Looking for %v in %v", needle, haystack)
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// expectState tests the results of IsAccessFile. It checks the state
// of the bool and whether the pointer is null or not (without
// checking the contents).
func expectState(t *testing.T, expectBool bool, expectPointer *path.Parsed, pathName upspin.PathName) {
	gotBool, gotPointer := access.IsAccessFile(pathName)
	if expectBool != gotBool {
		t.Fatalf("Expected %v, got %v", expectBool, gotBool)
	}
	if expectPointer != nil && gotPointer == nil {
		t.Fatal("Expected non-nil, got nil")
	}
	if expectPointer == nil && gotPointer != nil {
		t.Fatal("Expected nil, got non-nil")
	}
}
