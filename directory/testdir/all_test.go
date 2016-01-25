package testdir

import (
	"fmt"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/store/teststore"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Avoid networking for now.
type testAddrType struct{}

func (testAddrType) Network() string { return "test" }
func (testAddrType) String() string  { return "test:0.0.0.0" }

var testAddr testAddrType

const (
	user = "user@google.com"
	root = user + "/"
)

// TODO: Move these tests into the directory directory.
// TODO: Write a client and tests for the client interface.

func TestMakeRootDirectory(t *testing.T) {
	t.Logf("test addr: %s\n", testAddr)
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	loc, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("loc for root: %v\n", loc)
	// Fetch the directory back and inspect it.
	ciphertext, _, err := ss.Get(loc)
	if err != nil {
		t.Fatal("get directory:", err)
	}
	name, clear, err := teststore.UnpackBlob(ciphertext)
	if err != nil {
		t.Fatal("unpack:", err)
	}
	t.Logf("%q: [% x]\n", name, clear)
	if name != root {
		t.Fatalf("get of root: should have name %q; has %q", root, name)
	}
	if len(clear) != 0 {
		t.Fatalf("get of root: non-empty payload")
	}
}

func TestPutTopLevelFileUsingDirectory(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	loc, err := ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	// Fetch the data back and inspect it.
	ciphertext, _, err := ss.Get(loc)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	name, clear, err := teststore.UnpackBlob(ciphertext)
	if err != nil {
		t.Fatal("unpack:", err)
	}
	t.Logf("%q: [% x]\n", name, clear)
	if name != fileName {
		t.Fatalf("get of %q has name %q", fileName, name)
	}
	str := string(clear)
	if str != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
	}
}

const nFile = 100

func TestPutHundredTopLevelFilesUsingDirectory(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	locs := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		loc, err := ds.Put(fileName, []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
		locs[i] = loc
	}
	// Read them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		ciphertext, _, err := ss.Get(locs[j])
		if err != nil {
			t.Fatalf("%q: get blob: %v", fileName, err)
		}
		name, clear, err := teststore.UnpackBlob(ciphertext)
		if err != nil {
			t.Fatal("unpack:", err)
		}
		t.Logf("%q: [% x]\n", name, clear)
		if name != fileName {
			t.Fatalf("get of %q has name %q", fileName, name)
		}
		str := string(clear)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestGetHundredTopLevelFilesUsingDirectory(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		h, err := ds.Put(fileName, []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = h
	}
	// Get them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		entry, err := ds.Lookup(fileName)
		if err != nil {
			t.Fatalf("%q: lookup file: %v", fileName, err)
		}
		cipher, _, err := ss.Get(entry.Location)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		name, data, err := teststore.UnpackBlob(cipher)
		if err != nil {
			t.Fatalf("%q: unpack file: %v", fileName, err)
		}
		if name != fileName {
			t.Fatalf("%q: got wrong file name: %s", fileName, name)
		}
		str := string(data)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestCreateDirectoriesAndAFile(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	_, err = ds.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	_, err = ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err := ds.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup file: %v", fileName, err)
	}
	cipher, _, err := ss.Get(entry.Location)
	if err != nil {
		t.Fatalf("%q: get file: %v", fileName, err)
	}
	name, data, err := teststore.UnpackBlob(cipher)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	if name != fileName {
		t.Fatalf("%q: got wrong file name: %s", fileName, name)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	_, err = ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = ds.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: second lookup file: %v", fileName, err)
	}
	cipher, _, err = ss.Get(entry.Location)
	if err != nil {
		t.Fatalf("%q: second get file: %v", fileName, err)
	}
	name, data, err = teststore.UnpackBlob(cipher)
	if err != nil {
		t.Fatalf("%q: second unpack file: %v", fileName, err)
	}
	if name != fileName {
		t.Fatalf("%q: got wrong second file name: %s", fileName, name)
	}
	str = string(data)
	if str != text {
		t.Fatalf("after overwrite expected %q; got %q", text, str)
	}
}

/*
	Tree:

		user@google.com/
			ten
				eleven (file)
				twelve
					thirteen (file)
			twenty
				twentyone (file)
				twentytwo (file)
			thirty (dir)
*/

type globTest struct {
	// Strings all miss the leading "user@google.com" for brevity.
	pattern string
	files   []string
}

var globTests = []globTest{
	{"", []string{""}},
	{"*", []string{"ten", "twenty", "thirty"}},
	{"ten/eleven/thirteen", []string{}},
	{"ten/twelve/thirteen", []string{"ten/twelve/thirteen"}},
	{"ten/*", []string{"ten/twelve", "ten/eleven"}},
	{"ten/twelve/*", []string{"ten/twelve/thirteen"}},
	{"twenty/tw*", []string{"twenty/twentyone", "twenty/twentytwo"}},
	{"*/*", []string{"ten/twelve", "ten/eleven", "twenty/twentyone", "twenty/twentytwo"}},
}

func TestGlob(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	ds := NewService(ss)
	// Build the tree.
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make root:", err)
	}
	dirs := []string{
		"ten",
		"ten/twelve",
		"twenty",
		"thirty",
	}
	files := []string{
		"ten/eleven",
		"ten/twelve/thirteen",
		"twenty/twentyone",
		"twenty/twentytwo",
	}
	for _, dir := range dirs {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, dir))
		_, err := ds.MakeDirectory(name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, file))
		_, err := ds.Put(name, []byte(name))
		if err != nil {
			t.Fatalf("make file: %s: %v", name, err)
		}
	}
	// Now do the test proper.
	for _, test := range globTests {
		name := fmt.Sprintf("%s/%s", user, test.pattern)
		entries, err := ds.Glob(name)
		if err != nil {
			t.Errorf("%s: %v\n", test.pattern, err)
			continue
		}
		for i, f := range test.files {
			test.files[i] = fmt.Sprintf("%s/%s", user, f)
		}
		if len(test.files) != len(entries) {
			t.Errorf("%s: expected %d results; got %d:", test.pattern, len(test.files), len(entries))
			for _, e := range entries {
				t.Errorf("\t%q", e.Name)
			}
			continue
		}
		t.Log(test.files)
		// TODO: Order here needs to be sorted to match Glob output when that sorts.
		for i, f := range test.files {
			entry := entries[i]
			if string(entry.Name) != f {
				t.Errorf("%s: expected %q; got %q", test.pattern, f, entry.Name)
				continue
			}
			// Test that the ref gets the file.
			if !entry.Metadata.IsDir {
				data, err := ds.Fetch(entry.Location.Reference)
				if err != nil {
					t.Errorf("%s: %s: error reading data: %v", test.pattern, entry.Name, err)
					continue
				}
				str := string(data)
				if str != f {
					t.Errorf("%s: %s: wrong contents %q", test.pattern, entry.Name, str)
					continue
				}
			}
		}
	}
}
