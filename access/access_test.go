package acl_test

import (
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/acl"
	"upspin.googlesource.com/upspin.git/path"
)

func TestParse(t *testing.T) {
	aclText := `
  r : foo@bob.com ,a@b.co, x@y.uk // a comment

w:writerjoe@a.bc # comment r: ignored@incomment.com
a: m@n.mn  // other comment a: ignored@too.com
r:extra@reader.org`

	p, err := acl.Parse(aclText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{"foo@bob.com", "a@b.co", "x@y.uk", "extra@reader.org"})
	containsAll(t, p.Writers, []string{"writerjoe@a.bc"})
	containsAll(t, p.Appenders, []string{"m@n.mn"})
}

func TestParseEmptyFile(t *testing.T) {
	aclText := "\n # Just a comment.\n // Nothing to see here \n \n \n\t\n"
	p, err := acl.Parse(aclText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestParseContainsGroupName(t *testing.T) {
	aclText := "r: family,*@google.com,edpin@google.com/Groups/friends"
	p, err := acl.Parse(aclText)
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
		expectedErr = "unrecognized text in ACL file"
	)
	aclText := "read: bob@abc.com" // "read" is wrong. should be "r"
	_, err := acl.Parse(aclText)
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
	aclText := "//A comment\n #Another comment\n r: a@b.co : x"
	_, err := acl.Parse(aclText)
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
	aclText := "r: a@b.co r: c@b.co"
	_, err := acl.Parse(aclText)
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
	aclText := "r: notanemail/Group/family"
	p, err := acl.Parse(aclText)
	if err != nil {
		t.Fatal(err)
	}
	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
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
