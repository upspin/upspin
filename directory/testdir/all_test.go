package testdir

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	_ "upspin.googlesource.com/upspin.git/user/testuser" // TODO: Unused except in Setup

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/store/teststore"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Avoid networking for now.
const testAddr = "test:0.0.0.0"

const (
	user = "user@google.com"
	root = user + "/"
)

type Context string

func (c Context) Name() string {
	return string(c)
}

var _ upspin.ClientContext = (*Context)(nil)

type Setup struct {
	upspin.User
	upspin.Store
	upspin.Directory
}

func setup() (*Setup, error) {
	ctxt := Context("testcontext")
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   testAddr,
	}
	us, err := access.Switch.BindUser(ctxt, e)
	if err != nil {
		return nil, err
	}
	ds, err := access.Switch.BindDirectory(ctxt, e)
	if err != nil {
		return nil, err
	}
	// HACK: We set the store for the blobs to be the same as for the directory.
	return &Setup{
		User:      us,
		Store:     ds.(*Service).Store,
		Directory: ds,
	}, nil
}

func TestMakeRootDirectory(t *testing.T) {
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	loc, err := s.Directory.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("loc for root: %v\n", loc)
	// Fetch the directory back and inspect it.
	ciphertext, _, err := s.Store.Get(loc)
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
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	_, err = s.Directory.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	loc, err := s.Directory.Put(fileName, []byte(text), nil) // TODO.
	if err != nil {
		t.Fatal("put file:", err)
	}
	// Fetch the data back and inspect it.
	ciphertext, _, err := s.Store.Get(loc)
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
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	_, err = s.Directory.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	locs := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		loc, err := s.Directory.Put(fileName, []byte(text), nil) // TODO
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
		ciphertext, _, err := s.Store.Get(locs[j])
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
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	_, err = s.Directory.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		h, err := s.Directory.Put(fileName, []byte(text), nil) // TODO
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
		entry, err := s.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("%q: lookup file: %v", fileName, err)
		}
		cipher, _, err := s.Store.Get(entry.Location)
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
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	_, err = s.Directory.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	_, err = s.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	_, err = s.Directory.Put(fileName, []byte(text), nil) // TODO
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err := s.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup file: %v", fileName, err)
	}
	cipher, _, err := s.Store.Get(entry.Location)
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
	_, err = s.Directory.Put(fileName, []byte(text), nil) // TODO
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = s.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: second lookup file: %v", fileName, err)
	}
	cipher, _, err = s.Store.Get(entry.Location)
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
	s, err := setup()
	if err != nil {
		t.Fatal("setup:", err)
	}
	// Build the tree.
	_, err = s.Directory.MakeDirectory(user)
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
		_, err := s.Directory.MakeDirectory(name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, file))
		_, err := s.Directory.Put(name, []byte(name), nil) // TODO
		if err != nil {
			t.Fatalf("make file: %s: %v", name, err)
		}
	}
	// Now do the test proper.
	for _, test := range globTests {
		name := fmt.Sprintf("%s/%s", user, test.pattern)
		entries, err := s.Directory.Glob(name)
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
		// Sort so they match the output of Glob.
		sort.Strings(test.files)
		for i, f := range test.files {
			entry := entries[i]
			if string(entry.Name) != f {
				t.Errorf("%s: expected %q; got %q", test.pattern, f, entry.Name)
				continue
			}
			// Test that the ref gets the file.
			/* TODO			if !entry.Metadata.IsDir {
				data, err := s.Store.Get(entry.Location.Reference)
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
			*/
		}
	}
}
