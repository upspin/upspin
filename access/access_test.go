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

var empty = []string{}

var (
	accessText = []byte(`
  r : foo@bob.com ,a@b.co, x@y.uk # a comment

w:writer@a.bc # comment r: ignored@incomment.com
l: lister@n.mn  # other comment a: ignored@too.com
Read : reader@reader.org
# Some comment r: a: w: read: write ::::
WRITE: anotherwriter@a.bc
  create,DeLeTe  :admin@c.com`)
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

	match(t, p.Read, []string{"foo@bob.com", "a@b.co", "x@y.uk", "reader@reader.org"})
	match(t, p.Write, []string{"writer@a.bc", "anotherwriter@a.bc"})
	match(t, p.List, []string{"lister@n.mn"})
	match(t, p.Create, []string{"admin@c.com"})
	match(t, p.Delete, []string{"admin@c.com"})
}

func TestMallocs(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		access.Parse(accessFile, accessText)
	})
	t.Log("allocs:", allocs)
	if allocs != 29 {
		t.Fatal("expected 29 allocations, got ", allocs)
	}
}

func TestHasAccess(t *testing.T) {
	const (
		owner    = "foo@bob.com"
		pathName = owner + "/MyDir/myfile.txt"
	)
	var (
		readers    = []upspin.UserName{upspin.UserName("reader@r.com"), upspin.UserName("*@trustedreaders.com"), upspin.UserName("*@nsa.gov")}
		allReaders = []upspin.UserName{upspin.UserName("reader@r.com"), upspin.UserName("*")}
	)
	p, err := path.Parse(upspin.PathName(pathName))
	if err != nil {
		t.Fatal(err)
	}
	expectAccess(t, upspin.UserName(owner), true, p, readers)
	expectAccess(t, upspin.UserName("reader@r.com"), true, p, readers)
	expectAccess(t, upspin.UserName("buffoon@bozo.com"), false, p, readers)
	expectAccess(t, upspin.UserName("me@trustedreaders.com"), true, p, readers)
	expectAccess(t, upspin.UserName("snowden@nsa.gov"), true, p, readers)

	// Wildcard access.
	expectAccess(t, upspin.UserName("buffoon@bozo.com"), true, p, allReaders)
	expectAccess(t, upspin.UserName("reader@r.com"), true, p, allReaders)

	// No readers list.
	expectAccess(t, upspin.UserName(owner), true, p, nil)
	expectAccess(t, upspin.UserName("reader@r.com"), false, p, nil)

	// Now some errors for good form (and perfect line coverage).
	expectError(t, upspin.UserName("bad user name"), p, readers)
	expectError(t, upspin.UserName("a@b.com"), p, []upspin.UserName{"********@yo"})
	expectError(t, upspin.UserName("a@b.com"), p, []upspin.UserName{"*@mama@mia"})
}

func expectAccess(t *testing.T, user upspin.UserName, expectHasAccess bool, p path.Parsed, allowedAccess []upspin.UserName) {
	actuallyHasAccess, err := access.HasAccess(user, p, allowedAccess)
	if err != nil {
		t.Fatal(err)
	}
	if expectHasAccess != actuallyHasAccess {
		if expectHasAccess {
			t.Errorf("Expected user %s to have access", user)
		} else {
			t.Errorf("Expected user %s not to have access", user)
		}
	}
}

func expectError(t *testing.T, user upspin.UserName, p path.Parsed, allowedAccess []upspin.UserName) {
	_, err := access.HasAccess(user, p, allowedAccess)
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestParseEmptyFile(t *testing.T) {
	accessText := []byte("\n # Just a comment.\n\r\t # Nothing to see here \n \n \n\t\n")
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}

	match(t, p.Read, empty)
	match(t, p.Write, empty)
	match(t, p.List, empty)
	match(t, p.Create, empty)
	match(t, p.Delete, empty)
}

func TestParseContainsGroupName(t *testing.T) {
	accessText := []byte("r: family,*@google.com,edpin@google.com/Groups/friends")
	p, err := access.Parse(accessFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	// Group names such as "family" are currently ignored.
	// TODO: implement groups.
	match(t, p.Read, []string{"*@google.com"})
	match(t, p.Write, empty)
	match(t, p.List, empty)
	match(t, p.Create, empty)
	match(t, p.Delete, empty)
}

func TestParseWrongFormat1(t *testing.T) {
	const (
		expectedErr = accessFile + ":1: invalid right: \"rrrr\""
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
		expectedErr = accessFile + ":2: syntax error: invalid users list: "
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
		expectedErr = accessFile + ":1: syntax error: invalid rights"
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
		expectedErr = accessFile + ":1: invalid right: \"rea\""
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
		expectedErr = accessFile + ":1: syntax error: no colon on line: "
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
		expectedErr = accessFile + ":3: syntax error: invalid users list: "
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
	match(t, p.Read, empty)
	match(t, p.Write, empty)
	match(t, p.List, empty)
	match(t, p.Create, empty)
	match(t, p.Delete, empty)
}

func TestIsAccessFile(t *testing.T) {
	expectState(t, true, upspin.PathName("a@b.com/Access"))
	expectState(t, true, upspin.PathName("a@b.com/dir/subdir/Access"))
	expectState(t, false, upspin.PathName("a@b.com/dir/subdir/access"))
	expectState(t, false, upspin.PathName("a@b.com/dir/subdir/Access/")) // weird, but maybe ok?
	expectState(t, true, upspin.PathName("booboo/dir/subdir/Access"))    // more parsing is necessary
	expectState(t, false, upspin.PathName("not a path"))
}

// match requires the two slices to be equivalent, assuming no duplicates.
// The print of the path (ignoring the final / for a user name) must match the string.
func match(t *testing.T, want []path.Parsed, expect []string) {
	if len(want) != len(expect) {
		t.Fatalf("Expected %d paths %q, got %d: %v", len(expect), expect, len(want), want)
	}
	for _, path := range want {
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
