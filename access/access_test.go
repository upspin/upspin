package access_test

import (
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	accessFile = "me@here.com/Access"
)

var (
	accessText = []byte(`
  r : foo@bob.com ,a@b.co, x@y.uk # a comment

w:writerjoe@a.bc # comment r: ignored@incomment.com
a: m@n.mn  # other comment a: ignored@too.com
Read : extra@reader.org
# Some comment r: a: w: read: write ::::
WRITE: writerbob@a.bc
  aPPeNd  :appenderjohn@c.com`)
)

func BenchmarkParse(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_, err := access.Parse(accessFile, accessText)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestParse(t *testing.T) {
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{"foo@bob.com", "a@b.co", "x@y.uk", "extra@reader.org"})
	containsAll(t, p.Writers, []string{"writerjoe@a.bc", "writerbob@a.bc"})
	containsAll(t, p.Appenders, []string{"m@n.mn", "appenderjohn@c.com"})
}

func TestParseEmptyFile(t *testing.T) {
	accessText := []byte("\n # Just a comment.\n\r\t # Nothing to see here \n \n \n\t\n")
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}

	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestParseContainsGroupName(t *testing.T) {
	accessText := []byte("r: family,*@google.com,edpin@google.com/Groups/friends")
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	// Group names such as "family" are currently ignored.
	// TODO: implement groups.
	containsAll(t, p.Readers, []string{"*@google.com"})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestParseWrongFormat1(t *testing.T) {
	const (
		expectedErr = accessFile + ":1: unrecognized text: "
	)
	accessText := []byte("rrrr: bob@abc.com") // "rrrr" is wrong. should be just "r"
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat2(t *testing.T) {
	const (
		expectedErr = accessFile + ":2: unrecognized text: "
	)
	accessText := []byte("#A comment\n r: a@b.co : x")
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat3(t *testing.T) {
	const (
		expectedErr = accessFile + ":1: unrecognized text: "
	)
	accessText := []byte(": bob@abc.com") // missing access format text.
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat4(t *testing.T) {
	const (
		expectedErr = accessFile + ":1: unrecognized text: "
	)
	accessText := []byte("rea:bob@abc.com") // invalid access format text.
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseMissingAccessField(t *testing.T) {
	const (
		expectedErr = accessFile + ":1: unrecognized text: "
	)
	accessText := []byte("bob@abc.com")
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseTooManyFieldsOnSingleLine(t *testing.T) {
	const (
		expectedErr = accessFile + ":3: unrecognized text: "
	)
	accessText := []byte("\n\nr: a@b.co r: c@b.co")
	_, err := access.Parse(accessFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseBadPath(t *testing.T) {
	// TODO: Group names are being ignored. When implemented, this group name should cause an error.
	accessText := []byte("r: notanemail/Group/family")
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	containsAll(t, p.Readers, []string{})
	containsAll(t, p.Writers, []string{})
	containsAll(t, p.Appenders, []string{})
}

func TestIsAccessFile(t *testing.T) {
	expectState(t, true, upspin.PathName("a@b.com/Access"))
	expectState(t, true, upspin.PathName("a@b.com/dir/subdir/Access"))
	expectState(t, false, upspin.PathName("a@b.com/dir/subdir/access"))
	expectState(t, false, upspin.PathName("a@b.com/dir/subdir/Access/")) // weird, but maybe ok?
	expectState(t, true, upspin.PathName("booboo/dir/subdir/Access"))    // more parsing is necessary
	expectState(t, false, upspin.PathName("not a path"))
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

// expectState checks whether the results of IsAccessFile match with expectations and if not it fails the test.
func expectState(t *testing.T, expectIsFile bool, pathName upspin.PathName) {
	isFile := access.IsAccessFile(pathName)
	if expectIsFile != isFile {
		t.Fatalf("Expected %v, got %v", expectIsFile, isFile)
	}
}
