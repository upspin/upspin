// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package access

import (
	"reflect"
	"sort"
	"testing"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
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

	if a.IsReadableByAll() {
		t.Error("file is readable by all")
	}

	list := []string{"a@b.co", "foo@bob.com", "reader@reader.org", "x@y.uk"}
	match(t, a.List(Read), list)
	match(t, a.list[Read], list)
	list = []string{"anotherwriter@a.bc", "writer@a.bc"}
	match(t, a.List(Write), list)
	match(t, a.list[Write], list)
	list = []string{"lister@n.mn"}
	match(t, a.List(List), list)
	match(t, a.list[List], list)
	list = []string{"admin@c.com"}
	match(t, a.List(Create), list)
	match(t, a.list[Create], list)
	match(t, a.List(Delete), list)
	match(t, a.list[Delete], list)

	list = []string{"a@b.co", "foo@bob.com", "reader@reader.org", "x@y.uk", "anotherwriter@a.bc", "writer@a.bc", "lister@n.mn", "admin@c.com", "admin@c.com"}
	match(t, a.List(AnyRight), list)
	match(t, a.allUsers, list)
}

func TestParseEmpty(t *testing.T) {
	a, err := Parse(testFile, []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	for i := Read; i < numRights; i++ {
		match(t, a.list[i], nil)
		match(t, a.List(i), nil)
	}

	if a.IsReadableByAll() {
		t.Error("file is readable by all")
	}

	// Nil should be OK too.
	a, err = Parse(testFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := Read; i < numRights; i++ {
		match(t, a.list[i], nil)
	}
}

func TestParseAllUsers(t *testing.T) {
	// Granting "*" to another user should be OK.
	allUsersAccessText := []byte("* : foo@bob.com\nr: All")
	a, err := Parse(testFile, allUsersAccessText)
	if err != nil {
		t.Fatal(err)
	}

	if !a.IsReadableByAll() {
		t.Error("file is not readable by all")
	}

	match(t, a.list[Read], []string{"foo@bob.com", string(AllUsers)})
	foo := []string{"foo@bob.com"}
	match(t, a.list[Write], foo)
	match(t, a.list[List], foo)
	match(t, a.list[Create], foo)
	match(t, a.list[Delete], foo)

	// Should also work if we give "*" to all.
	allUsersAccessText = []byte("* : foo@bob.com\n*: All")
	a, err = Parse(testFile, allUsersAccessText)
	if err != nil {
		t.Fatal(err)
	}

	if !a.IsReadableByAll() {
		t.Error("file is not readable by all")
	}

	fooAll := []string{"foo@bob.com", string(AllUsers)}
	match(t, a.list[Read], fooAll)
	match(t, a.list[Write], fooAll)
	match(t, a.list[List], fooAll)
	match(t, a.list[Create], fooAll)
	match(t, a.list[Delete], fooAll)
}

func TestParseAllUsersBad(t *testing.T) {
	// Here we have "all" with another explicit reader, and should see an error.
	// "ALL@UPSPIN.IO" will be canonicalized when parsed.
	allUsersAccessTextBad := []byte("r : foo@bob.com ALL")
	_, err := Parse(testFile, allUsersAccessTextBad)
	expectedErr := errors.E(errors.Invalid, errors.Str(`"ALL" cannot appear with other users`))
	if !errors.Match(expectedErr, err) {
		t.Fatalf(`unexpected error for "all" not alone: %v`, err)
	}
}

func TestCannotUseReservedUser(t *testing.T) {
	allUsersAccessText := []byte("r:all@upspin.IO")
	_, err := Parse(testFile, allUsersAccessText)
	if !errors.Match(errors.E(errors.Invalid, errors.Str(`reserved user name "all@upspin.IO"`)), err) {
		t.Fatal(err)
	}
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
		if a1.equal(a2) != test.expect {
			t.Errorf("%d: equal(%q, %q) should be %t, is not", i, test.path1, test.path2, test.expect)
		}
	}
}

func TestParseGroup(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	group, err := ParseGroup(parsed, groupText)
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
	if allocs != 23 {
		t.Fatal("expected 23 allocations, got ", allocs)
	}
}

func TestGroupParseAllocs(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		ParseGroup(parsed, groupText)
	})
	t.Log("allocs:", allocs)
	if allocs != 6 {
		t.Fatal("expected 6 allocations, got ", allocs)
	}
}

func TestHasAccessNoGroups(t *testing.T) {
	const (
		owner = upspin.UserName("me@here.com")

		// This access file defines readers and writers but no other rights.
		text = "l: *@google.com\n" +
			"r: reader@r.com, reader@foo.bar, *@nsa.gov\n" +
			"w: writer@foo.bar\n"
	)
	a, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatal(err)
	}

	check := func(user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		ok, missing, err := canWithMissing(a, user, right, file)
		if len(missing) > 0 {
			t.Fatalf("expected no missing groups: %v", missing)
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

	// The "AnyRight" right check also works for everyone.
	check(owner, AnyRight, "me@here.com/foo/bar", true)
	check("writer@foo.bar", AnyRight, "me@here.com/foo/bar", true)
	check("reader@foo.bar", AnyRight, "me@here.com/foo/bar", true)
	check("writer@foo.bar", AnyRight, "me@here.com/foo/bar", true)
	check("not@a.person", AnyRight, "me@here.com/foo/bar", false)

	// The "AnyRight" right check also works for Access files.
	check(owner, AnyRight, "me@here.com/Access", true)
	check("writer@foo.bar", AnyRight, "me@here.com/Access", true)
	check("reader@foo.bar", AnyRight, "me@here.com/Access", true)
	check("writer@foo.bar", AnyRight, "me@here.com/Access", true)
	check("not@a.person", AnyRight, "me@here.com/Access", false)

	// Wildcard that should match.
	check("joe@nsa.gov", Read, "me@here.com/foo/barx", true)
	check("joe@nsa.gov", AnyRight, "me@here.com/foo/barx", true)
	check("joe@nsa.gov", Read, "me@here.com/Access", true)
	check("joe@nsa.gov", AnyRight, "me@here.com/Access", true)
	check("bob@google.com", List, "me@here.com/", true)
	check("ana@google.com", AnyRight, "me@here.com/", true)

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
	resetGroupsCache()

	const (
		// This access file defines readers and writers but no other rights.
		accessText = "r: reader@r.com, reader@foo.bar, family\n" +
			"w: writer@foo.bar\n" +
			"d: family"

		// This access file mentions a group that does not exist.
		missingGroupAccessText = "r: aMissingGroup, family\n"

		missingGroupName = upspin.PathName("me@here.com/Group/aMissingGroup")
	)

	loadTest := func(name upspin.PathName) ([]byte, error) {
		switch name {
		case "me@here.com/Group/family":
			return []byte("# My family\n sister@me.com, brother@me.com\n"), nil
		default:
			return nil, errors.Errorf("%s not found", name)
		}
	}

	a, err := Parse(testFile, []byte(accessText))
	if err != nil {
		t.Fatal(err)
	}

	check := func(user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		t.Helper()
		ok, err := a.Can(user, right, file, loadTest)
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

	// AnyRight works for groups.
	check("sister@me.com", AnyRight, "me@here.com/foo/bar", true)

	err = RemoveGroup("me@here.com/Group/family")
	if err != nil {
		t.Fatal(err)
	}
	// Sister can't read anymore and family group is needed.
	ok, missing, err := canWithMissing(a, "sister@me.com", Read, "me@here.com/foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("Expected no permission")
	}
	if len(missing) != 1 {
		t.Fatalf("expected one missing groups: %v", missing)
	}

	// Now operate on the Access file that mentions a non-existent group.
	a, err = Parse(testFile, []byte(missingGroupAccessText))
	if err != nil {
		t.Fatal(err)
	}

	// Family group should work.
	check("sister@me.com", Read, "me@here.com/foo/bar", true)

	// Unknown member should get an error about the missing group.
	check("aunt@me.com", Read, "me@here.com/foo/bar", false)
	if errors.Match(errors.E(missingGroupName), err) {
		t.Errorf("expected error about aMissingGroup, got %v", err)
	}
}

func TestAccessAllUsers(t *testing.T) {
	const (
		owner = upspin.UserName("me@here.com")

		// This access file defines a single writer but allows anyone to read.
		text = "r: All\n" +
			"w: writer@foo.bar\n"
	)
	a, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatal(err)
	}

	check := func(user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		t.Helper()
		ok, missing, err := canWithMissing(a, user, right, file)
		if len(missing) > 0 {
			t.Fatalf("expected no missing groups: %v", missing)
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

	// Owner can read.
	check(owner, Read, "me@here.com/foo/bar", true)

	// Random user can read (because of "all"==AllUsers).
	check("someone@obscure.com", Read, "me@here.com/foo/bar", true)

	// Owner cannot write.
	check(owner, Write, "me@here.com/foo/bar", false)

	// Writer can write.
	check("writer@foo.bar", Write, "me@here.com/foo/bar", true)

	// Unpermitted others cannot write.
	check("someone@obscure.com", Write, "me@here.com/foo/bar", false)
}

func TestGroupDisallowsAll(t *testing.T) {
	parsed, err := path.Parse("me@here.com/Group/meAndAllElse")
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		"all",
		"ALL",
		"all@upspin.io",
		"all@UPSPIN.io",
		"me@here.com all",
		"all@upspin.io\nme@here.com",
	}
	for _, test := range tests {
		_, err = ParseGroup(parsed, []byte(test))
		if err == nil {
			t.Errorf(`group parse accepted "all" in: %s`, test)
			continue
		}
		expectedErr := errors.E(parsed.Path(), errors.Invalid)
		if !errors.Match(expectedErr, err) {
			t.Errorf(`unexpected error %v in: %s`, err, test)
		}
	}
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

func TestParseStar(t *testing.T) {
	accessText := []byte("*: joe@blow.com")
	a, err := Parse(testFile, accessText)
	if err != nil {
		t.Fatal(err)
	}
	joe := []string{"joe@blow.com"}
	match(t, a.list[Read], joe)
	match(t, a.list[Write], joe)
	match(t, a.list[List], joe)
	match(t, a.list[Create], joe)
	match(t, a.list[Delete], joe)
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

func TestParseBadRight(t *testing.T) {
	expectedErr := errors.E(upspin.PathName(testFile), errors.Invalid, errors.Str("invalid access rights on line 1: \"rrrr\""))

	accessText := []byte("rrrr: bob@abc.com") // "rrrr" is wrong. should be just "r"
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want %s", err, expectedErr)
	}
}

func TestParseExtraColon(t *testing.T) {
	expectedErr := errors.E(upspin.PathName(testFile), errors.Invalid, errors.Str(`invalid users list on line 2: "a@b.co : x"`))
	accessText := []byte("#A comment\n r: a@b.co : x")
	_, err := Parse(testFile, accessText)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want %s", err, expectedErr)
	}
}

type invalidTest struct {
	text     string
	errorStr string
}

var invalidAccessFileTests = []invalidTest{
	// No right or colon.
	{"bob@abc.com", `no colon on line 1: "bob@abc.com"`},
	// No right.
	{": bob@abc.com", `invalid rights list on line 1: ""`},
	// Misspelled right.
	{"rea:bob@abc.com", `invalid access rights on line 1: "rea"`},
	// Bad UTF-8.
	{"r:ren\xe9e@abc.com", `invalid users list on line 1: "ren\xe9e@abc.com"`},
	// Unprintable group name.
	{"r:abc\x04de", `invalid users list on line 1: "abc\x04de"`},
	// Too many fields.
	{"\n\nr: a@b.co r: c@b.co", `invalid users list on line 3: "a@b.co r: c@b.co"`},
	// Bad group path.
	{"r: notanemail/Group/family", `bad user name in group path "notanemail/Group/family"`},
}

func TestInvalidParse(t *testing.T) {
	for _, test := range invalidAccessFileTests {
		accessText := []byte(test.text)
		_, err := Parse(testFile, accessText)
		if err == nil {
			t.Fatal("Expected error, got none")
		}
		expectedErr := errors.E(upspin.PathName(testFile), errors.Invalid, errors.Str(test.errorStr))
		if !errors.Match(expectedErr, err) {
			t.Errorf("given %q: err = %s, want %s", test.text, err, expectedErr)
		}
	}
}

func TestParseBadGroupFile(t *testing.T) {
	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	// Multiple commas not allowed.
	_, err = ParseGroup(parsed, []byte("joe@me.com ,, fred@me.com"))
	if err == nil {
		t.Error("expected error with multiple commas, got none")
	}
	// Bad external group file name (invalid user).
	_, err = ParseGroup(parsed, []byte("joe@me.com, fred@me.com/Group/fred@me.com"))
	if err == nil {
		t.Error("expected error for bad group file name, got none")
	}
	// Bad local group file name (invalid user).
	_, err = ParseGroup(parsed, []byte("joe@me.com, *"))
	if err == nil {
		t.Error("expected error for bad local group file name, got none")
	}
}

func TestParseBadGroupMember(t *testing.T) {
	expectedErr := errors.E(upspin.PathName(testGroupFile), errors.Invalid,
		errors.Str(`bad group users list on line 1: user.Parse: user fred@: invalid operation: missing domain name`))

	parsed, err := path.Parse(testGroupFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseGroup(parsed, []byte("joe@me.com, fred@"))
	if err == nil {
		t.Fatal("expected error, got none")
	}
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want %s", err, expectedErr)
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
	if !a.equal(b) {
		t.Error("Marshal/Nnmarshal failed to recover Access file")
		t.Errorf("Original: %v\n", a)
		t.Errorf("Recovered: %v\n", b)
	}
}

func TestNew(t *testing.T) {
	const path = upspin.PathName("bob@foo.com/my/private/sub/dir/Access")
	a, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := Parse(path, []byte("r,w,d,c,l: bob@foo.com"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.equal(expected) {
		t.Errorf("Expected %v to equal %v", a, expected)
	}
}

func TestUsersNoGroupLoad(t *testing.T) {
	acc, err := Parse("bob@foo.com/Access",
		[]byte("r: sue@foo.com, tommy@foo.com, joe@foo.com\nw: bob@foo.com, family"))
	if err != nil {
		t.Fatal(err)
	}
	readersList, groupsNeeded, err := usersWithMissing(acc, Read)
	if err != nil {
		t.Fatalf("Expected no error, got %s", err)
	}
	if len(groupsNeeded) != 0 {
		t.Errorf("Expected no groups, got %d", len(groupsNeeded))
	}
	expectedReaders := []string{"bob@foo.com", "sue@foo.com", "tommy@foo.com", "joe@foo.com"}
	expectEqual(t, expectedReaders, listFromUserName(readersList))
	writersList, groupsNeeded, err := usersWithMissing(acc, Write)
	if err != nil {
		t.Fatalf("Expected no error; got %s", err)
	}
	if groupsNeeded == nil {
		t.Fatalf("Expected groups to be needed")
	}
	expectedWriters := []string{"bob@foo.com"}
	expectEqual(t, expectedWriters, listFromUserName(writersList))
	groupsExpected := []string{"bob@foo.com/Group/family"}
	expectEqual(t, groupsExpected, listFromPathName(groupsNeeded))
	// Add the missing group.
	err = AddGroup("bob@foo.com/Group/family", []byte("sis@foo.com, uncle@foo.com, grandparents"))
	if err != nil {
		t.Fatal(err)
	}
	// Try again.
	writersList, groupsNeeded, err = usersWithMissing(acc, Write)
	if err != nil {
		t.Fatalf("Round 2: Expected no error %s", err)
	}
	if groupsNeeded == nil {
		t.Fatalf("Round 2: Expected groups to be needed")
	}
	groupsExpected = []string{"bob@foo.com/Group/grandparents"}
	expectEqual(t, groupsExpected, listFromPathName(groupsNeeded))
	expectedWriters = []string{"bob@foo.com", "sis@foo.com", "uncle@foo.com"}
	expectEqual(t, expectedWriters, listFromUserName(writersList))
	// Add grandparents and for good measure, add the family again.
	err = AddGroup("bob@foo.com/Group/grandparents", []byte("grandpamoe@antifoo.com family"))
	if err != nil {
		t.Fatal(err)
	}
	writersList, groupsNeeded, err = usersWithMissing(acc, Write)
	if err != nil {
		t.Fatal(err)
	}
	if groupsNeeded != nil {
		t.Fatalf("Round 3: Expected no groups to be needed, got %v", groupsNeeded)
	}
	expectedWriters = []string{"bob@foo.com", "sis@foo.com", "uncle@foo.com", "grandpamoe@antifoo.com"}
	expectEqual(t, expectedWriters, listFromUserName(writersList))
}

func TestUsersNoGroupLoad2(t *testing.T) {
	// Should find two missing groups, colleagues and neighbors. Neighbors
	// should not be lost even though colleagues appears twice, once at root
	// level and once at leaf level.
	acc, err := Parse("bob@foo.com/Access",
		[]byte("r: colleagues, acquaintances"))
	if err != nil {
		t.Fatal(err)
	}
	// Add top group.
	err = AddGroup("bob@foo.com/Group/acquaintances", []byte("colleagues, neighbors"))
	if err != nil {
		t.Fatal(err)
	}
	_, groupsNeeded, err := usersWithMissing(acc, Read)
	if err != nil {
		t.Fatalf("Expected no error, got %s", err)
	}
	if len(groupsNeeded) != 2 {
		t.Errorf("Expected two missing groups, got %d", len(groupsNeeded))
	}
}

func TestUsersNoGroupLoad3(t *testing.T) {
	// Should find two reading members, bob and jan.
	// Verify that members of a second group (jan in this case) are not lost
	// track of just because they appear after a group that matches the top
	// level search (groupa in this case).
	acc, err := Parse("bob@foo.com/Access",
		[]byte("r: groupa groupb"))
	if err != nil {
		t.Fatal(err)
	}
	// Add groups.
	err = AddGroup("bob@foo.com/Group/groupa", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = AddGroup("bob@foo.com/Group/groupb", []byte("groupa, jan@foo.com"))
	if err != nil {
		t.Fatal(err)
	}
	readersList, groupsNeeded, err := usersWithMissing(acc, Read)
	if err != nil {
		t.Fatalf("Expected no error, got %s", err)
	}
	if len(groupsNeeded) != 0 {
		t.Errorf("Expected no missing groups, got %d", len(groupsNeeded))
	}
	expectedReaders := []string{"bob@foo.com", "jan@foo.com"}
	expectEqual(t, expectedReaders, listFromUserName(readersList))
}

func usersCheck(t *testing.T, right Right, load func(upspin.PathName) ([]byte, error), file upspin.PathName, data []byte, expected []string) {
	t.Helper()
	resetGroupsCache()
	acc, err := Parse(file, data)
	if err != nil {
		t.Fatal(err)
	}
	list, err := acc.Users(right, load)
	if err != nil {
		t.Fatalf("Expected no error, got %s", err)
	}
	expectEqual(t, expected, listFromUserName(list))
}

func TestUsers(t *testing.T) {
	loaded := false
	loadTest := func(name upspin.PathName) ([]byte, error) {
		loaded = true
		switch name {
		case "bob@foo.com/Group/friends":
			return []byte("nancy@foo.com, anna@foo.com"), nil
		default:
			return nil, errors.Errorf("%s not found", name)
		}
	}

	usersCheck(t, Read, loadTest, "bob@foo.com/Access",
		[]byte("r: bob@foo.com, sue@foo.com, tommy@foo.com, joe@foo.com, friends"),
		[]string{"bob@foo.com", "sue@foo.com", "tommy@foo.com", "joe@foo.com", "nancy@foo.com", "anna@foo.com"})
	if !loaded {
		t.Fatalf("group file was not loaded")
	}

	// Retry with owner left out of Access.
	usersCheck(t, Read, loadTest, "bob@foo.com/Access",
		[]byte("r: sue@foo.com, tommy@foo.com, joe@foo.com, friends"),
		[]string{"bob@foo.com", "sue@foo.com", "tommy@foo.com", "joe@foo.com", "nancy@foo.com", "anna@foo.com"})

	// Retry with repeated readers and no group.
	usersCheck(t, Read, loadTest, "bob@foo.com/Access",
		[]byte("r: al@foo.com, sue@foo.com, bob@foo.com, tommy@foo.com, al@foo.com"),
		[]string{"bob@foo.com", "sue@foo.com", "tommy@foo.com", "al@foo.com"})

	// Retry with empty Access.
	usersCheck(t, Read, loadTest, "bob@foo.com/Access",
		[]byte(""),
		[]string{"bob@foo.com"})

	// Check that everyone is in the "AnyRight" list.
	usersCheck(t, AnyRight, loadTest, "bob@foo.com/Access",
		[]byte("r: al@foo.com, sue@foo.com, bob@foo.com, tommy@foo.com bob@foo.com/Group/friends"),
		[]string{"al@foo.com", "anna@foo.com", "bob@foo.com", "nancy@foo.com", "sue@foo.com", "tommy@foo.com"})

}

func TestUsersAndCan(t *testing.T) {
	accessOwner := upspin.PathName("foo@foo.com")
	loadFiles := make(map[string]string)
	loadFiles["foo@foo.com/Group/foogroup"] = "b@foo.com, c@foo.com"
	loadFiles["bar@bar.com/Group/bargroup"] = ""
	loadFiles["bar@bar.com/Group/self"] = "bar@bar.com"

	// Create three group files from different users that create a cycle
	loadFiles["a@a.aa/Group/a2b2c"] = "b@b.bb/Group/b2c2a"
	loadFiles["b@b.bb/Group/b2c2a"] = "c@c.cc/Group/c2a2b"
	loadFiles["c@c.cc/Group/c2a2b"] = "a@a.aa/Group/a2b2c"

	load := func(name upspin.PathName) ([]byte, error) {
		data, found := loadFiles[string(name)]
		if found {
			return []byte(data), nil
		}
		return nil, errors.Errorf("%s not found", name)
	}

	cantload := func(name upspin.PathName) ([]byte, error) {
		return nil, errors.Errorf("cantload should not have been called")
	}

	checkParse := func(path upspin.PathName, data []byte) *Access {
		t.Helper()
		a, err := Parse(path, data)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	checkUsers := func(a *Access, right Right, expected []string) {
		t.Helper()
		resetGroupsCache()
		list, err := a.Users(right, load) // once with load
		if err != nil {
			t.Errorf("Expected no error, got %s", err)
			return
		}
		expectEqual(t, expected, listFromUserName(list))

		// Nothing should be left to load, run Users again.
		list, err = a.Users(right, cantload) // now with cantload
		if err != nil {
			t.Errorf("Expected no error, got %s", err)
			return
		}
		expectEqual(t, expected, listFromUserName(list))
	}

	checkCan := func(a *Access, user upspin.UserName, right Right, file upspin.PathName, truth bool) {
		t.Helper()
		resetGroupsCache()
		ok, err := a.Can(user, right, file, load) // once with load
		if ok != truth {
			if err != nil {
				t.Error(err)
			} else if ok {
				t.Errorf("%s can %s %s", user, right, file)
			} else {
				t.Errorf("%s cannot %s %s", user, right, file)
			}
		}

		// Nothing should be left to load, run Can again.
		ok, err = a.Can(user, right, file, cantload) // now with cantload
		if ok != truth {
			if err != nil {
				t.Error(err)
			} else if ok {
				t.Errorf("%s can %s %s", user, right, file)
			} else {
				t.Errorf("%s cannot %s %s", user, right, file)
			}
		}
	}

	const F = "foo@foo.com"
	const B = "bar@bar.com"
	list_foo := []string{F}

	// Test with an empty Access file.
	a := checkParse(accessOwner, []byte(`
			# empty Access file`))
	checkUsers(a, Read, list_foo)
	checkUsers(a, List, list_foo)
	checkUsers(a, Write, nil)
	checkUsers(a, Create, nil)
	checkUsers(a, Delete, nil)
	// Owner can always read or list anything in their tree.
	checkCan(a, F, Read, F+"/Access", true)
	checkCan(a, F, Read, F+"/Group", true)
	checkCan(a, F, Read, F+"/madeup", true)
	checkCan(a, F, List, F+"/Access", true)
	checkCan(a, F, List, F+"/Group", true)
	checkCan(a, F, List, F+"/madeup", true)
	// Owner can always write their own Access file.
	checkCan(a, F, Write, F+"/Access", true)
	checkCan(a, F, Write, F+"/deeper/Access", true)
	// Owner cannot write own Group or other files without explicit Access rights granted.
	checkCan(a, F, Write, F+"/Group", false)
	checkCan(a, F, Write, F+"/madeup", false)
	checkCan(a, F, Create, F+"/Access", true)
	checkCan(a, F, Create, F+"/Group", false)
	checkCan(a, F, Create, F+"/madeup", false)
	checkCan(a, F, Delete, F+"/Access", true)
	checkCan(a, F, Delete, F+"/Group", false)
	checkCan(a, F, Delete, F+"/madeup", false)

	// Test with a simple Access file that has only owner listed for everything.
	a = checkParse(accessOwner, []byte(`
			read:	foo@foo.com
			write:	foo@foo.com
			list:	foo@foo.com
			create: foo@foo.com
			delete:	foo@foo.com
			`))
	checkUsers(a, Read, list_foo)
	checkUsers(a, List, list_foo)
	checkUsers(a, Write, list_foo)
	checkUsers(a, Create, list_foo)
	checkUsers(a, Delete, list_foo)
	checkCan(a, F, Read, F+"/Access", true)
	checkCan(a, F, Read, F+"/Group", true)
	checkCan(a, F, Read, F+"/madeup", true)
	checkCan(a, F, List, F+"/Access", true)
	checkCan(a, F, List, F+"/Group", true)
	checkCan(a, F, List, F+"/madeup", true)
	checkCan(a, F, Write, F+"/Access", true)
	checkCan(a, F, Write, F+"/deeper/Access", true)
	checkCan(a, F, Write, F+"/Group", true)  // now true
	checkCan(a, F, Write, F+"/madeup", true) // now true
	checkCan(a, F, Create, F+"/Access", true)
	checkCan(a, F, Create, F+"/Group", true)  // now true
	checkCan(a, F, Create, F+"/madeup", true) // now true
	checkCan(a, F, Delete, F+"/Access", true)
	checkCan(a, F, Delete, F+"/Group", true)  // now true
	checkCan(a, F, Delete, F+"/madeup", true) // now true

	// Test with a simple Access file that has only another listed for everything.
	a = checkParse(accessOwner, []byte(`
			read:	bar@bar.com
			write:	bar@bar.com
			list:	bar@bar.com
			create: bar@bar.com
			delete:	bar@bar.com
			`))
	list_bar := []string{B}
	list_bar_foo := []string{B, F}
	checkUsers(a, Read, list_bar_foo)
	checkUsers(a, List, list_bar_foo)
	checkUsers(a, Write, list_bar)
	checkUsers(a, Create, list_bar)
	checkUsers(a, Delete, list_bar)
	checkCan(a, B, Read, F+"/Access", true)
	checkCan(a, B, Read, F+"/Group", true)
	checkCan(a, B, Read, F+"/madeup", true)
	checkCan(a, B, List, F+"/Access", true)
	checkCan(a, B, List, F+"/Group", true)
	checkCan(a, B, List, F+"/madeup", true)
	checkCan(a, B, Write, F+"/Access", false)        // only owner can write Access
	checkCan(a, B, Write, F+"/deeper/Access", false) // only owner can write Access
	checkCan(a, B, Write, F+"/Group", true)          // now true
	checkCan(a, B, Write, F+"/madeup", true)         // now true
	checkCan(a, B, Create, F+"/Access", false)       // only owner can create Access
	checkCan(a, B, Create, F+"/Group", true)         // now true
	checkCan(a, B, Create, F+"/madeup", true)        // now true
	checkCan(a, B, Delete, F+"/Access", false)       // only owner can delete Access
	checkCan(a, B, Delete, F+"/Group", true)
	checkCan(a, B, Delete, F+"/madeup", true)

	// Test with a simple Access file that has only another's non-existing group listed for everything.
	a = checkParse(accessOwner, []byte(`
			read:	bar@bar.com/Group/madeup
			write:	bar@bar.com/Group/madeup
			list:	bar@bar.com/Group/madeup
			create: bar@bar.com/Group/madeup
			delete:	bar@bar.com/Group/madeup
			`))
	// checkUsers(a, Read, list_bar_foo) // These tests would fail for now with the 'not found' error.
	// checkUsers(a, List, list_bar_foo) // Perhaps Users() should return a partial list despite network
	// checkUsers(a, Write, list_bar)    // errors and not found errors. Can() does.
	// checkUsers(a, Create, list_bar)
	// checkUsers(a, Delete, list_bar)
	checkCan(a, B, Read, F+"/Access", true)          // same as in set above
	checkCan(a, B, Read, F+"/Group", true)           // same as in set above
	checkCan(a, B, Read, F+"/madeup", true)          // same as in set above
	checkCan(a, B, List, F+"/Access", true)          // same as in set above
	checkCan(a, B, List, F+"/Group", true)           // same as in set above
	checkCan(a, B, List, F+"/madeup", true)          // same as in set above
	checkCan(a, B, Write, F+"/Access", false)        // same as in set above
	checkCan(a, B, Write, F+"/deeper/Access", false) // same as in set above
	checkCan(a, B, Write, F+"/Group", true)          // same as in set above
	checkCan(a, B, Write, F+"/madeup", true)         // same as in set above
	checkCan(a, B, Create, F+"/Access", false)       // same as in set above
	checkCan(a, B, Create, F+"/Group", true)         // same as in set above
	checkCan(a, B, Create, F+"/madeup", true)        // same as in set above
	checkCan(a, B, Delete, F+"/Access", false)       // same as in set above
	checkCan(a, B, Delete, F+"/Group", true)         // same as in set above
	checkCan(a, B, Delete, F+"/madeup", true)        // same as in set above

	// Test with a simple Access file that has only another's existing group listed that includes self.
	a = checkParse(accessOwner, []byte(`
			read:	bar@bar.com/Group/self
			write:	bar@bar.com/Group/self
			list:	bar@bar.com/Group/self
			create: bar@bar.com/Group/self
			delete:	bar@bar.com/Group/self
			`))
	checkUsers(a, Read, list_bar_foo)
	checkUsers(a, List, list_bar_foo)
	checkUsers(a, Write, list_bar)
	checkUsers(a, Create, list_bar)
	checkUsers(a, Delete, list_bar)
	checkCan(a, B, Read, F+"/Access", true)          // same as in set above
	checkCan(a, B, Read, F+"/Group", true)           // same as in set above
	checkCan(a, B, Read, F+"/madeup", true)          // same as in set above
	checkCan(a, B, List, F+"/Access", true)          // same as in set above
	checkCan(a, B, List, F+"/Group", true)           // same as in set above
	checkCan(a, B, List, F+"/madeup", true)          // same as in set above
	checkCan(a, B, Write, F+"/Access", false)        // same as in set above
	checkCan(a, B, Write, F+"/deeper/Access", false) // same as in set above
	checkCan(a, B, Write, F+"/Group", true)          // same as in set above
	checkCan(a, B, Write, F+"/madeup", true)         // same as in set above
	checkCan(a, B, Create, F+"/Access", false)       // same as in set above
	checkCan(a, B, Create, F+"/Group", true)         // same as in set above
	checkCan(a, B, Create, F+"/madeup", true)        // same as in set above
	checkCan(a, B, Delete, F+"/Access", false)       // same as in set above
	checkCan(a, B, Delete, F+"/Group", true)         // same as in set above
	checkCan(a, B, Delete, F+"/madeup", true)        // same as in set above

	// Test with a simple Access file that contains a group cycle for each right.
	a = checkParse(accessOwner, []byte(`
			read:	b@b.bb/Group/b2c2a
			write:	b@b.bb/Group/b2c2a
			list:	b@b.bb/Group/b2c2a
			create: b@b.bb/Group/b2c2a
			delete:	b@b.bb/Group/b2c2a
			`))
	list_abc_foo := []string{"a@a.aa", "b@b.bb", "c@c.cc", F}
	list_abc := []string{"a@a.aa", "b@b.bb", "c@c.cc"}
	checkUsers(a, Read, list_abc_foo)
	checkUsers(a, List, list_abc_foo)
	checkUsers(a, Write, list_abc)
	checkUsers(a, Create, list_abc)
	checkUsers(a, Delete, list_abc)
	checkCan(a, "a@a.aa", Read, F+"/Access", true)          // same as in set above
	checkCan(a, "a@a.aa", Read, F+"/Group", true)           // same as in set above
	checkCan(a, "a@a.aa", Read, F+"/madeup", true)          // same as in set above
	checkCan(a, "a@a.aa", List, F+"/Access", true)          // same as in set above
	checkCan(a, "a@a.aa", List, F+"/Group", true)           // same as in set above
	checkCan(a, "a@a.aa", List, F+"/madeup", true)          // same as in set above
	checkCan(a, "a@a.aa", Write, F+"/Access", false)        // same as in set above
	checkCan(a, "a@a.aa", Write, F+"/deeper/Access", false) // same as in set above
	checkCan(a, "a@a.aa", Write, F+"/Group", true)          // same as in set above
	checkCan(a, "a@a.aa", Write, F+"/madeup", true)         // same as in set above
	checkCan(a, "a@a.aa", Create, F+"/Access", false)       // same as in set above
	checkCan(a, "a@a.aa", Create, F+"/Group", true)         // same as in set above
	checkCan(a, "a@a.aa", Create, F+"/madeup", true)        // same as in set above
	checkCan(a, "a@a.aa", Delete, F+"/Access", false)       // same as in set above
	checkCan(a, "a@a.aa", Delete, F+"/Group", true)         // same as in set above
	checkCan(a, "a@a.aa", Delete, F+"/madeup", true)        // same as in set above

	a = checkParse(accessOwner, []byte(`
			read:	bar@bar.com/Group/bargroup
			write:	bar@bar.com/Group/bargroup
			# list:
			# create:
			# delete:
			`))

	checkUsers(a, Read, []string{F, B})     // check that group owner is given same Read right as the group itself
	checkCan(a, B, Read, F+"/madeup", true) // check that group owner is given same Read right as the group itself

	checkUsers(a, Write, []string{B})        // check that group owner is given same Write right as the group itself
	checkCan(a, B, Write, F+"/madeup", true) // check that group owner is given same Write right as the group itself

	checkCan(a, F, Read, "Access", true)
	checkCan(a, "jan@foo.com", Read, "Access", false)

}

func TestIsAccessFile(t *testing.T) {
	tests := []struct {
		name     upspin.PathName
		isAccess bool
	}{
		{"a@b.com/Access", true},
		{"a@b.com/foo/bar/Access", true},
		{"a@b.com/NotAccess", false},
		{"a@b.com/Group/Access", true},
		{"a@b.com//Access/", true},     // Extra slashes don't matter.
		{"a@b.com//Access/foo", false}, //Access must not be a directory.
		{"/Access/foo", false},         // No user.
	}
	for _, test := range tests {
		isAccess := IsAccessFile(test.name)
		if isAccess == test.isAccess {
			continue
		}
		if isAccess {
			t.Errorf("%q is not an access file; IsAccessFile says it is", test.name)
		}
		if !isAccess {
			t.Errorf("%q is an access file; IsAccessFile says not", test.name)
		}
	}
}

func TestIsGroupFile(t *testing.T) {
	tests := []struct {
		name    upspin.PathName
		isGroup bool
	}{
		{"a@b.com/Group/foo", true},
		{"a@b.com/Group/foo/bar", true},
		{"a@b.com/Group/Access", false}, // Access file is not a Group file.
		{"a@b.com/Group/Access/bar", true},
		{"a@b.com/Group/foo/Access", false}, // It's an Access file.
		{"a@b.com//Group/", false},          // No file.
		{"a@b.com//Group/foo", true},        // Extra slashes don't matter.
		{"a@b.com/foo/Group", false},        // Group directory must be in root.
		{"/Group/foo", false},               // No user.
	}
	for _, test := range tests {
		isGroup := IsGroupFile(test.name)
		if isGroup == test.isGroup {
			continue
		}
		if isGroup {
			t.Errorf("%q is not a group file; IsGroupFile says it is", test.name)
		}
		if !isGroup {
			t.Errorf("%q is a group file; IsGroupFile says not", test.name)
		}
	}
}

func TestIsAccessControlFile(t *testing.T) {
	tests := []struct {
		name            upspin.PathName
		isAccessControl bool
	}{
		{"a@b.com/Access", true},
		{"a@b.com/foo/bar/Access", true},
		{"a@b.com/NotAccess", false},
		{"a@b.com/Group/Access", true},
		{"a@b.com//Access/", true},     // Extra slashes don't matter.
		{"a@b.com//Access/foo", false}, //Access must not be a directory.
		{"/Access/foo", false},         // No user.
		{"a@b.com/Group/foo", true},
		{"a@b.com/Group/foo/bar", true},
		{"a@b.com/Group/Access", true},
		{"a@b.com/Group/Access/bar", true},
		{"a@b.com/Group/foo/Access", true},
		{"a@b.com//Group/", false},   // No file.
		{"a@b.com//Group/foo", true}, // Extra slashes don't matter.
		{"a@b.com/foo/Group", false}, // Group directory must be in root.
		{"/Group/foo", false},        // No user.
	}
	for _, test := range tests {
		isAccessControl := IsAccessControlFile(test.name)
		if isAccessControl == test.isAccessControl {
			continue
		}
		if isAccessControl {
			t.Errorf("%q is not an access control file; IsAccessControlFile says it is", test.name)
		}
		if !isAccessControl {
			t.Errorf("%q is an access control file file; IsAccessControlFile says not", test.name)
		}
	}
}

// match requires the two slices to be equivalent, assuming no duplicates.
// The print of the path (ignoring the final / for a user name) must match the string.
// The lists are sorted, because Access.Parse sorts them.
func match(t *testing.T, want []path.Parsed, expect []string) {
	t.Helper()
	if len(want) != len(expect) {
		t.Fatalf("Expected %d paths %q, got %d: %v", len(expect), expect, len(want), want)
	}
	for i, path := range want {
		var compare string
		if path.IsRoot() {
			compare = string(path.User())
		} else {
			compare = path.String()
		}
		if compare != expect[i] {
			t.Errorf("User %s not found in at position %d in list", compare, i)
			t.Errorf("expect: %q; got %q", expect, want)
		}
	}
}

// expectEqual fails if the two lists do not have the same contents, irrespective of order.
func expectEqual(t *testing.T, expected []string, gotten []string) {
	t.Helper()
	sort.Strings(expected)
	sort.Strings(gotten)
	if len(expected) != len(gotten) {
		t.Errorf("Length mismatched, expected %d, got %d: %v vs %v", len(expected), len(gotten), expected, gotten)
		return
	}
	if len(expected) > 0 {
		if !reflect.DeepEqual(expected, gotten) {
			t.Errorf("Expected %v got %v", expected, gotten)
			return
		}
	}
}

func listFromPathName(p []upspin.PathName) []string {
	ret := make([]string, len(p))
	for i, v := range p {
		ret[i] = string(v)
	}
	return ret
}

func listFromUserName(u []upspin.UserName) []string {
	ret := make([]string, len(u))
	for i, v := range u {
		ret[i] = string(v)
	}
	return ret
}

// equal reports whether a and b have equal contents.
func (a *Access) equal(b *Access) bool {
	if a.parsed.Compare(b.parsed) != 0 {
		return false
	}
	if len(a.list) != len(b.list) {
		return false
	}
	for i, al := range a.list {
		bl := b.list[i]
		if len(al) != len(bl) {
			return false
		}
		for j, ar := range al {
			if ar.Compare(bl[j]) != 0 {
				return false
			}
		}
	}
	return true
}

// canWithMissing reports results from access.Can() along with groups that were missing.
func canWithMissing(a *Access, user upspin.UserName, right Right, file upspin.PathName) (bool, []upspin.PathName, error) {
	var missing []upspin.PathName

	// This load() parameter has the side effect of adding the missing pathnames to the
	// globals.
	granted, err := a.Can(user, right, file, func(p upspin.PathName) ([]byte, error) {
		missing = append(missing, p)
		return []byte{}, nil
	})

	// Restore global groups to its prior state.
	for _, p := range missing {
		_ = RemoveGroup(p)
	}

	return granted, missing, err
}

// usersWithMissing reports results from access.Users() along with groups that were missing.
func usersWithMissing(a *Access, right Right) ([]upspin.UserName, []upspin.PathName, error) {
	var missing []upspin.PathName

	// This load() parameter has the side effect of adding the missing pathnames to the
	// globals.
	users, err := a.Users(right, func(p upspin.PathName) ([]byte, error) {
		missing = append(missing, p)
		return []byte{}, nil
	})

	// Restore global groups to its prior state.
	for _, p := range missing {
		_ = RemoveGroup(p)
	}

	return users, missing, err
}

// resetGroupsCache sets the global groups variable back to its starting point.
func resetGroupsCache() {
	groups = make(map[upspin.PathName][]path.Parsed) // Forget any existing groups in the cache.
}
