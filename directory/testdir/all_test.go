package testdir

// This test uses an in-process Store service for the underlying
// storage. To run this test against a GCP Store, start a GCP store
// locally and run this test with flag
// -use_gcp_store=http://localhost:8080. It may take up to a minute
// to run.

import (
	"flag"
	"fmt"
	"sort"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

var (
	useGCPStore = "" // leave empty for in-process. see init below
)

var context *upspin.Context

func packData() upspin.PackData {
	return []byte{upspin.DebugPack}
}

func setupContext() {
	if context != nil {
		return
	}

	storeEndpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	if strings.HasPrefix(useGCPStore, "http") {
		storeEndpoint.Transport = upspin.GCP
		storeEndpoint.NetAddr = upspin.NetAddr(useGCPStore)
	}

	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	// TODO: This bootstrapping is fragile and will break. It depends on the order of setup.
	context = new(upspin.Context)
	context.KeyPair = upspin.KeyPair{
		Private: []byte("privacy in the privy"),
	}
	context.Packing = upspin.DebugPack // TODO.
	var err error
	context.User, err = bind.User(context, endpoint)
	if err != nil {
		panic(err)
	}
	context.Store, err = bind.Store(context, storeEndpoint)
	if err != nil {
		panic(err)
	}
	context.Directory, err = bind.Directory(context, endpoint)
	if err != nil {
		panic(err)
	}
}

func setup(userName upspin.UserName) {
	setupContext()
	context.UserName = userName
	err := context.User.(*testuser.Service).Install(userName, context.Directory)
	if err != nil {
		panic(err)
	}
}

func TestPutTopLevelFileUsingDirectory(t *testing.T) {
	const (
		user = "user1@google.com"
		root = user + "/"
	)
	setup(user)
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	loc, err := context.Directory.Put(fileName, []byte(text), packData())
	if err != nil {
		t.Fatal("put file:", err)
	}

	// Test that Lookup returns the same location.
	entry, err := context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup %s: %s", fileName, err)
	}
	if loc != entry.Location {
		t.Errorf("Lookup's reference does not match Put's reference:\t%v\n\t%v", loc, entry.Location)
	}

	// Fetch the data back and inspect it.
	ciphertext, locs, err := context.Store.Get(loc.Reference.Key)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	if locs != nil {
		ciphertext, _, err = context.Store.Get(locs[0].Reference.Key)
		if err != nil {
			t.Fatal("get redirected blob:", err)
		}
	}
	clear, err := unpackBlob(context, ciphertext, fileName, &entry.Metadata)
	if err != nil {
		t.Fatal("unpack:", err)
	}
	str := string(clear)
	if str != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
	}
}

const nFile = 100

func TestPutHundredTopLevelFilesUsingDirectory(t *testing.T) {
	const (
		user = "user2@google.com"
		root = user + "/"
	)
	setup(user)
	// Create a hundred files.
	locs := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		loc, err := context.Directory.Put(fileName, []byte(text), packData())
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
		ciphertext, newLocs, err := context.Store.Get(locs[j].Reference.Key)
		if err != nil {
			t.Fatalf("%q: get blob: %v, key: %v", fileName, err, locs[j].Reference.Key)
		}
		if newLocs != nil {
			ciphertext, _, err = context.Store.Get(newLocs[0].Reference.Key)
			if err != nil {
				t.Fatalf("%q: get redirected blob: %v", fileName, err)
			}
		}
		entry, err := context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		clear, err := unpackBlob(context, ciphertext, fileName, &entry.Metadata)
		if err != nil {
			t.Fatal("unpack:", err)
		}
		str := string(clear)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestGetHundredTopLevelFilesUsingDirectory(t *testing.T) {
	const (
		user = "user3@google.com"
		root = user + "/"
	)
	setup(user)
	// Create a hundred files.
	href := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		h, err := context.Directory.Put(fileName, []byte(text), packData())
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
		entry, err := context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("#%d: %q: lookup file: %v", i, fileName, err)
		}
		cipher, locs, err := context.Store.Get(entry.Location.Reference.Key)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		if locs != nil {
			cipher, _, err = context.Store.Get(locs[0].Reference.Key)
			if err != nil {
				t.Fatalf("%q: get redirected file: %v", fileName, err)
			}
		}
		entry, err = context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		data, err := unpackBlob(context, cipher, fileName, &entry.Metadata)
		if err != nil {
			t.Fatalf("%q: unpack file: %v", fileName, err)
		}
		str := string(data)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestCreateDirectoriesAndAFile(t *testing.T) {
	const (
		user = "user4@google.com"
		root = user + "/"
	)
	setup(user)
	_, err := context.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = context.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = context.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = context.Directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	_, err = context.Directory.Put(fileName, []byte(text), packData())
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err := context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup file: %v", fileName, err)
	}
	cipher, locs, err := context.Store.Get(entry.Location.Reference.Key)
	if err != nil {
		t.Fatalf("%q: get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = context.Store.Get(locs[0].Reference.Key)
		if err != nil {
			t.Fatalf("%q: get redirected file: %v", fileName, err)
		}
	}
	data, err := unpackBlob(context, cipher, fileName, &entry.Metadata)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	_, err = context.Directory.Put(fileName, []byte(text), packData())
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: second lookup file: %v", fileName, err)
	}
	cipher, locs, err = context.Store.Get(entry.Location.Reference.Key)
	if err != nil {
		t.Fatalf("%q: second get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = context.Store.Get(locs[0].Reference.Key)
		if err != nil {
			t.Fatalf("%q: second get redirected file: %v", fileName, err)
		}
	}
	data, err = unpackBlob(context, cipher, fileName, &entry.Metadata)
	if err != nil {
		t.Fatalf("%q: second unpack file: %v", fileName, err)
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
	const (
		user = "user5@google.com"
		root = user + "/"
	)
	setup(user)
	// Build the tree.
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
		t.Log(name)
		_, err := context.Directory.MakeDirectory(name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, file))
		_, err := context.Directory.Put(name, []byte(name), packData())
		if err != nil {
			t.Fatalf("make file: %s: %v", name, err)
		}
	}
	// Now do the test proper.
	for _, test := range globTests {
		name := fmt.Sprintf("%s/%s", user, test.pattern)
		entries, err := context.Directory.Glob(name)
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
		}
	}
}

func init() {
	flag.StringVar(&useGCPStore, "use_gcp_store", "", "leave empty to use an in-process Store, or set to the URL of the GCP store (e.g. 'http://localhost:8080')")
}
