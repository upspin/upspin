package access

import (
	"fmt"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	testFile      = "me@here.com/Access"
	testGroupFile = "me@here.com/Group/family"
)

var empty = []string{}

var (
	accessText = []byte(`
  r : foo@bob.com ,a@b.co x@y.uk # a comment. Notice commas and spaces.

w:writer@a.bc # comment r: ignored@incomment.com
l: lister@n.mn  # other comment a: ignored@too.com
Read : reader@reader.org
# Some comment r: a: w: read: write ::::
WRITE: anotherwriter@a.bc
  create,DeLeTe  :admin@c.com`)

	groupText = []byte("#This is my family\nfred@me.com, ann@me.com\njoe@me.com\n")
)

func BenchmarkParse(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_, err := Parse(testFile, accessText)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestParse(t *testing.T) {
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}

	match(t, a.list[Read], []string{"a@b.co", "foo@bob.com", "reader@reader.org", "x@y.uk"})
	match(t, a.list[Write], []string{"anotherwriter@a.bc", "writer@a.bc"})
	match(t, a.list[List], []string{"lister@n.mn"})
	match(t, a.list[Create], []string{"admin@c.com"})
	match(t, a.list[Delete], []string{"admin@c.com"})
}

type accessEqualTest struct {
	path1   upspin.PathName
	access1 string
	path2   upspin.PathName
	access2 string
	expect  bool
}

var accessEqualTests = []accessEqualTest{
	{
		// Same, but formatted differently. Parse and sort will fix.
		"a1@b.com/Access",
		"r:joe@foo.com, fred@foo.com\n",
		"a1@b.com/Access",
		"# A comment\nr:fred@foo.com, joe@foo.com\n",
		true,
	},
	{
		// Different names.
		"a1@b.com/Access",
		"r:joe@foo.com, fred@foo.com\n",
		"a2@b.com/Access",
		"# A comment\nr:fred@foo.com, joe@foo.com\n",
		false,
	},
	{
		// Same name, different contents.
		"a1@b.com/Access",
		"r:joe@foo.com, fred@foo.com\n",
		"a1@b.com/Access",
		"# A comment\nr:fred@foo.com, zot@foo.com\n",
		false,
	},
}

func TestAccessEqual(t *testing.T) {
	for i, test := range accessEqualTests {
		a1, err := Parse(test.path1, []byte(test.access1))
		if err != nil {
			t.Fatalf("%d: %s: %s\n", i, test.path1, err)
		}
		a2, err := Parse(test.path2, []byte(test.access2))
		if err != nil {
			t.Fatalf("%d: %s: %s\n", i, test.path2, err)
		}
		if a1.Equal(a2) != test.expect {
			t.Errorf("%d: equal(%q, %q) should be %t, is not", i, test.path1, test.path2, test.expect)
		}
	}
}

func TestParseGroup(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	group, err := parseGroup(parsed, groupText)
	if err != nil {
		t.Fatal(err)
	}

	match(t, group, []string{"fred@me.com", "ann@me.com", "joe@me.com"})
}

func TestParseAllocs(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		Parse(testFile, accessText)
	})
	t.Log("allocs:", allocs)
	// TODO: Why so many? (5 are for sorting the rights list.)
	if allocs != 36 {
		t.Fatal("expected 36 allocations, got ", allocs)
	}
}

func TestGroupParseAllocs(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		parseGroup(parsed, groupText)
	})
	t.Log("allocs:", allocs)
	// TODO: Why so many?
	if allocs != 11 {
		t.Fatal("expected 11 allocations, got ", allocs)
	}
}

func TestHasAccessNoGroups(t *testing.T) {
	const (
		owner = upspin.UserName("me@here.com")

		// This access file defines readers and writers but no other rights.
		text = "r: reader@r.com, reader@foo.bar, *@nsa.gov\n" +
			"w: writer@foo.bar\n"
	)
	a, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatal(err)
	}

	check := func(user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		ok, groups, err := a.Can(user, right, file)
		if groups != nil {
			t.Fatalf("non-empty groups %q", groups)
		}
		if err != nil {
			t.Fatal(err)
		}
		if ok == truth {
			return
		}
		if ok {
			t.Errorf("%s can %s %s", user, right, file)
		} else {
			t.Errorf("%s cannot %s %s", user, right, file)
		}
	}

	// Owner can read anything and write Access files.
	check(owner, Read, "me@here.com/foo/bar", true)
	check(owner, Read, "me@here.com/foo/Access", true)
	check(owner, List, "me@here.com/foo/bar", true)
	check(owner, Create, "me@here.com/foo/Access", true)
	check(owner, Write, "me@here.com/foo/Access", true)

	// Permitted others can read.
	check("reader@foo.bar", Read, "me@here.com/foo/bar", true)

	// Unpermitted others cannot read.
	check("writer@foo.bar", List, "me@here.com/foo/bar", false)

	// Permitted others can write.
	check("writer@foo.bar", Write, "me@here.com/foo/bar", true)

	// Unpermitted others cannot write.
	check("reader@foo.bar", Write, "me@here.com/foo/bar", false)

	// Non-owners cannot list (it's not in the Access file).
	check("reader@foo.bar", List, "me@here.com/foo/bar", false)
	check("writer@foo.bar", List, "me@here.com/foo/bar", false)

	// No one can create (it's not in the Access file).
	check(owner, Create, "me@here.com/foo/bar", false)
	check("writer@foo.bar", Create, "me@here.com/foo/bar", false)

	// No one can delete (it's not in the Access file).
	check(owner, Delete, "me@here.com/foo/bar", false)
	check("writer@foo.bar", Delete, "me@here.com/foo/bar", false)

	// Wildcard that should match.
	check("joe@nsa.gov", Read, "me@here.com/foo/bar", true)

	// Wildcard that should not match.
	check("*@nasa.gov", Read, "me@here.com/foo/bar", false)

	// User can write Access file.
	check(owner, Write, "me@here.com/foo/Access", true)

	// User can write Group file.
	check(owner, Write, "me@here.com/Group/bar", true)

	// Other user cannot write Access file.
	check("writer@foo.bar", Write, "me@here.com/foo/Access", false)

	// Other user cannot write Group file.
	check("writer@foo.bar", Write, "me@here.com/Group/bar", false)
}

// This is a simple test of basic group functioning. We still need a proper full-on test with
// a populated tree.
func TestHasAccessWithGroups(t *testing.T) {
	groups = make(map[upspin.PathName][]path.Parsed) // Forget any existing groups in the cache.

	const (
		owner = upspin.UserName("me@here.com")

		// This access file defines readers and writers but no other rights.
		accessText = "r: reader@r.com, reader@foo.bar, family\n" +
			"w: writer@foo.bar\n" +
			"d: family"

		// This is a simple group for a family.
		groupName = upspin.PathName("me@here.com/Group/family")
		groupText = "# My family\n sister@me.com, brother@me.com\n"
	)
	a, err := Parse(testFile, []byte(accessText))
	if err != nil {
		t.Fatal(err)
	}

	loadedGroup := false

	check := func(user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		ok, missingGroups, err := a.Can(user, right, file)
		if missingGroups != nil {
			// This is a simple test. There's only one group.
			if len(missingGroups) != 1 {
				t.Fatalf("expected one missing group, got %v", missingGroups)
			}
			pathName := missingGroups[0]
			if pathName != groupName {
				t.Fatalf("expected %q for group name, got %q", groupName, pathName)
			}
			if loadedGroup {
				t.Fatal("group already loaded")
			}
			err = a.AddGroup(groupName, []byte(groupText))
			if err != nil {
				t.Fatal(err)
			}
			loadedGroup = true
			// It must work now.
			ok, missingGroups, err = a.Can(user, right, file)
			if err != nil {
				t.Fatal(err)
			}
		}
		if ok == truth {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("%s can %s %s", user, right, file)
		} else {
			t.Errorf("%s cannot %s %s", user, right, file)
		}
	}

	// Permitted group can read.
	check("sister@me.com", Read, "me@here.com/foo/bar", true)

	// Unknown member cannot read.
	check("aunt@me.com", Read, "me@here.com/foo/bar", false)

	// Group cannot write.
	check("sister@me.com", Write, "me@here.com/foo/bar", false)

	// The owner of a group is a member of the group.
	check("me@here.com", Delete, "me@here.com/foo/bar", true)
}

func TestParseEmptyFile(t *testing.T) {
	accessText := []byte("\n # Just a comment.\n\r\t # Nothing to see here \n \n \n\t\n")
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}

	match(t, a.list[Read], empty)
	match(t, a.list[Write], empty)
	match(t, a.list[List], empty)
	match(t, a.list[Create], empty)
	match(t, a.list[Delete], empty)
}

func TestParseContainsGroupName(t *testing.T) {
	accessText := []byte("r: family,*@google.com,edpin@google.com/Group/friends")
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	match(t, a.list[Read], []string{"*@google.com", "edpin@google.com/Group/friends", "me@here.com/Group/family"})
	match(t, a.list[Write], empty)
	match(t, a.list[List], empty)
	match(t, a.list[Create], empty)
	match(t, a.list[Delete], empty)
}

func TestParseWrongFormat1(t *testing.T) {
	const (
		expectedErr = testFile + ":1: invalid right: \"rrrr\""
	)
	accessText := []byte("rrrr: bob@abc.com") // "rrrr" is wrong. should be just "r"
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat2(t *testing.T) {
	const (
		expectedErr = testFile + ":2: syntax error: invalid users list: "
	)
	accessText := []byte("#A comment\n r: a@b.co : x")
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat3(t *testing.T) {
	const (
		expectedErr = testFile + ":1: syntax error: invalid rights"
	)
	accessText := []byte(": bob@abc.com") // missing access format text.
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseWrongFormat4(t *testing.T) {
	const (
		expectedErr = testFile + ":1: invalid right: \"rea\""
	)
	accessText := []byte("rea:bob@abc.com") // invalid access format text.
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseMissingAccessField(t *testing.T) {
	const (
		expectedErr = testFile + ":1: syntax error: no colon on line: "
	)
	accessText := []byte("bob@abc.com")
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseTooManyFieldsOnSingleLine(t *testing.T) {
	const (
		expectedErr = testFile + ":3: syntax error: invalid users list: "
	)
	accessText := []byte("\n\nr: a@b.co r: c@b.co")
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Expected prefix %s, got %s", expectedErr, err)
	}
}

func TestParseBadGroupPath(t *testing.T) {
	accessText := []byte("r: notanemail/Group/family")
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("expected error, got none")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Fatalf("expected group error, got: %v", err)
	}
}

func TestParseBadGroupFile(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	// Multiple commas not allowed.
	_, err = parseGroup(parsed, []byte("joe@me.com ,, fred@me.com"))
	if err == nil {
		t.Fatal("expected error, got none")
	}
}

func TestParseBadGroupMember(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseGroup(parsed, []byte("joe@me.com, fred@"))
	if err == nil {
		t.Fatal("expected error, got none")
	}
	if !strings.Contains(err.Error(), "no user name") {
		t.Fatalf("expected missing user name error, got: %v", err)
	}
}

func TestMarshal(t *testing.T) {
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := a.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := UnmarshalJSON(testFile, buf)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, a, b)
}

// This is unusual, but to be safe we are asserting equal correctly we test that our comparator is good.
// (Is worth making Equal a method in Access? Not needed outside of this test yet.)
func TestAssertEqual(t *testing.T) {
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := a.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := UnmarshalJSON(upspin.PathName("me@there.com/random/Access"), buf)
	if err != nil {
		t.Fatal(err)
	}
	diff := differenceString(a, b)
	// Verify failure
	expected := "Owners don't match: me@here.com != me@there.com"
	if diff != expected {
		t.Fatalf("Expected error %s, got %s", expected, diff)
	}

	// Tweak a to force a failure.
	p, err := path.Parse("hello@here.com/foo")
	a.list[Read][1] = p // We know a.list has 3 entries.
	b, err = UnmarshalJSON(testFile, buf)
	if err != nil {
		t.Fatal(err)
	}
	diff = differenceString(a, b)
	// Verify failure
	expected = "Missing hello@here.com/foo in list b for right read"
	if diff != expected {
		t.Fatalf("Expected error %s, got %s", expected, diff)
	}
}

func assertEqual(t *testing.T, a, b *Access) {
	if str := differenceString(a, b); str != "" {
		t.Fatal(str)
	}
}

// differenceString returns a string describing the high-level differences between a and b or
// an empty string if they are equal.
func differenceString(a, b *Access) string {
	if len(a.list) != len(b.list) {
		return fmt.Sprintf("Lists of rights not equal length: %d != %d", len(a.list), len(b.list))
	}
	if len(a.list) != int(numRights) {
		return fmt.Sprintf("Lists of rights not equal to length of rights %d != %d", len(a.list), numRights)
	}
	for r, al := range a.list { // for each right r
		right := Right(r)
		bl := b.list[right]
		if len(al) != len(bl) {
			return fmt.Sprintf("Lists for right %s not equal length: %d != %d", right, len(al), len(bl))
		}
		bChecked := make([]int, len(bl)) // list of times each entry in b was visited.
	Outer:
		for _, pa := range al {
			for i := 0; i < len(bl); i++ {
				pb := bl[i]
				if pa.Equal(pb) {
					bChecked[i]++
					continue Outer
				}
			}
			return fmt.Sprintf("Missing %s in list b for right %s", pa, right)
		}
		for i, b := range bChecked {
			if b != 1 {
				return fmt.Sprintf("%s appears %d times, expected 1", bl[i], b)
			}
		}
	}
	if a.owner != b.owner {
		return fmt.Sprintf("Owners don't match: %s != %s", a.owner, b.owner)
	}
	if a.domain != b.domain {
		return fmt.Sprintf("Domains don't match: %s != %s", a.domain, b.domain)
	}
	if !a.parsed.Equal(b.parsed) {
		return fmt.Sprintf("Names don't match: %s != %s", a.parsed, b.parsed)
	}
	return ""
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
// The lists are sorted, because Access.Parse sorts them.
func match(t *testing.T, want []path.Parsed, expect []string) {
	if len(want) != len(expect) {
		t.Fatalf("Expected %d paths %q, got %d: %v", len(expect), expect, len(want), want)
	}
	for i, path := range want {
		var compare string
		if len(path.Elems) == 0 {
			compare = string(path.User)
		} else {
			compare = path.String()
		}
		if compare != expect[i] {
			t.Errorf("User %s not found in at position %d in list", compare, i)
			t.Errorf("expect: %q; got %q", expect, want)
		}
	}
}

// expectState checks whether the results of IsAccessFile match with expectations and if not it fails the test.
func expectState(t *testing.T, expectIsFile bool, pathName upspin.PathName) {
	isFile := IsAccessFile(pathName)
	if expectIsFile != isFile {
		t.Fatalf("Expected %v, got %v", expectIsFile, isFile)
	}
}
