package service

import (
	"fmt"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/sim/directory"
	"upspin.googlesource.com/upspin.git/sim/path"
	"upspin.googlesource.com/upspin.git/sim/ref"
	"upspin.googlesource.com/upspin.git/sim/store"
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

func TestMakeRootDirectory(t *testing.T) {
	t.Logf("test addr: %s\n", testAddr)
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
	r, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("Ref for root: %v\n", r)
	// Fetch the directory back and inspect it.
	ciphertext, err := ss.Get(r.Reference)
	if err != nil {
		t.Fatal("get directory:", err)
	}
	name, clear, err := store.UnpackBlob(ciphertext)
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

func TestPutTopLevelFile(t *testing.T) {
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	href, err := ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	// Fetch the data back and inspect it.
	ciphertext, err := ss.Get(href.Reference)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	name, clear, err := store.UnpackBlob(ciphertext)
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

func TestPutHundredTopLevelFiles(t *testing.T) {
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]ref.HintedReference, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := path.Name(fmt.Sprintf("%s/file.%d", user, i))
		h, err := ds.Put(fileName, []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = h
	}
	// Read them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := path.Name(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		ciphertext, err := ss.Get(href[j].Reference)
		if err != nil {
			t.Fatalf("%q: get blob: %v", fileName, err)
		}
		name, clear, err := store.UnpackBlob(ciphertext)
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

func TestGetHundredTopLevelFiles(t *testing.T) {
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]ref.HintedReference, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := path.Name(fmt.Sprintf("%s/file.%d", user, i))
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
		fileName := path.Name(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		h, data, err := ds.Get(fileName)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		if h != href[j] {
			t.Fatalf("%q: get file: bad hash", fileName)
		}
		str := string(data)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestCreateDirectoriesAndAFile(t *testing.T) {
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	_, err = ds.MakeDirectory(path.Name(fmt.Sprintf("%s/foo/", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(path.Name(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(path.Name(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.MakeDirectory(path.Name(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := path.Name(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	ref, err := ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	nref, data, err := ds.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if ref != nref {
		t.Fatal("ref mismatch")
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	ref, err = ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	nref, data, err = ds.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if ref != nref {
		t.Fatal("after overwrite ref mismatch")
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
	ss := store.NewService(ref.Location{Addr: testAddr})
	ds := directory.NewService(ss)
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
		name := path.Name(fmt.Sprintf("%s/%s", user, dir))
		_, err := ds.MakeDirectory(name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := path.Name(fmt.Sprintf("%s/%s", user, file))
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
			if !entry.IsDir {
				data, err := ds.Fetch(entry.Ref.Reference)
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
