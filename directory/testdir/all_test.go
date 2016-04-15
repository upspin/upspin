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
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
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
		Private: upspin.PrivateKey("privacy in the privy"),
	}
	context.Packing = upspin.DebugPack
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

func packData(t *testing.T, data []byte, entry *upspin.DirEntry) ([]byte, upspin.PackData) {
	packer := pack.Lookup(context.Packing)
	if packer == nil {
		t.Fatalf("Packer is nil for packing %d", context.Packing)
	}

	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(context, data, entry)
	if cipherLen < 0 {
		t.Fatalf("PackLen failed for %q", entry.Name)
	}
	cipher := make([]byte, cipherLen)
	n, err := packer.Pack(context, cipher, data, entry)
	if err != nil {
		t.Fatal(err)
	}
	return cipher[:n], entry.Metadata.PackData
}

func storeData(t *testing.T, data []byte, name upspin.PathName) *upspin.DirEntry {
	// TODO: we'd really like a path.Clean.
	parsed, err := path.Parse(name)
	if err != nil {
		panic(err) // Really shouldn't happen here.
	}
	name = parsed.Path()
	entry := &upspin.DirEntry{
		Name: name,
		Metadata: upspin.Metadata{
			IsDir:    false,
			Size:     uint64(len(data)),
			Time:     upspin.Now(),
			PackData: []byte{byte(upspin.DebugPack)},
		},
	}
	cipher, packdata := packData(t, data, entry)
	ref, err := context.Store.Put(cipher)
	if err != nil {
		t.Fatal(err)
	}
	entry.Location = upspin.Location{
		Endpoint:  context.Store.Endpoint(),
		Reference: ref,
	}
	entry.Metadata.PackData = packdata
	return entry
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

	entry1 := storeData(t, []byte(text), fileName)
	err, _ := context.Directory.Put(entry1)
	if err != nil {
		t.Fatal("put file:", err)
	}

	// Test that Lookup returns the same location.
	entry2, err := context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup %s: %s", fileName, err)
	}
	if entry1.Location != entry2.Location {
		t.Errorf("Lookup's location does not match Put's location:\t%v\n\t%v", entry1.Location, entry2.Location)
	}

	// Fetch the data back and inspect it.
	ciphertext, locs, err := context.Store.Get(entry1.Location.Reference)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	if locs != nil {
		ciphertext, _, err = context.Store.Get(locs[0].Reference)
		if err != nil {
			t.Fatal("get redirected blob:", err)
		}
	}
	clear, err := unpackBlob(context, ciphertext, entry1)
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
		entry := storeData(t, []byte(text), fileName)
		err, _ := context.Directory.Put(entry)
		if err != nil {
			t.Fatal("put file:", err)
		}
		locs[i] = entry.Location
	}
	// Read them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		ciphertext, newLocs, err := context.Store.Get(locs[j].Reference)
		if err != nil {
			t.Fatalf("%q: get blob: %v, ref: %v", fileName, err, locs[j].Reference)
		}
		if newLocs != nil {
			ciphertext, _, err = context.Store.Get(newLocs[0].Reference)
			if err != nil {
				t.Fatalf("%q: get redirected blob: %v", fileName, err)
			}
		}
		entry, err := context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		clear, err := unpackBlob(context, ciphertext, entry)
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
		entry := storeData(t, []byte(text), fileName)
		err, _ := context.Directory.Put(entry)
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = entry.Location
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
		cipher, locs, err := context.Store.Get(entry.Location.Reference)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		if locs != nil {
			cipher, _, err = context.Store.Get(locs[0].Reference)
			if err != nil {
				t.Fatalf("%q: get redirected file: %v", fileName, err)
			}
		}
		entry, err = context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		data, err := unpackBlob(context, cipher, entry)
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
	entry := storeData(t, []byte(text), fileName)
	err, _ = context.Directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup file: %v", fileName, err)
	}
	cipher, locs, err := context.Store.Get(entry.Location.Reference)
	if err != nil {
		t.Fatalf("%q: get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = context.Store.Get(locs[0].Reference)
		if err != nil {
			t.Fatalf("%q: get redirected file: %v", fileName, err)
		}
	}
	data, err := unpackBlob(context, cipher, entry)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	entry = storeData(t, []byte(text), fileName)
	err, _ = context.Directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: second lookup file: %v", fileName, err)
	}
	cipher, locs, err = context.Store.Get(entry.Location.Reference)
	if err != nil {
		t.Fatalf("%q: second get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = context.Store.Get(locs[0].Reference)
		if err != nil {
			t.Fatalf("%q: second get redirected file: %v", fileName, err)
		}
	}
	data, err = unpackBlob(context, cipher, entry)
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
		entry := storeData(t, []byte(name), name)
		err, _ := context.Directory.Put(entry)
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

func TestSequencing(t *testing.T) {
	const (
		user     = "user6@google.com"
		fileName = user + "/file"
	)
	setup(user)
	// Validate sequence increases after write.
	seq := int64(-1)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, []byte(text), fileName)
		err, _ := context.Directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err = context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup file %d: %v", i, err)
		}
		if entry.Metadata.Sequence <= seq {
			t.Fatalf("sequence file %d did not increase: old seq %d; new seq %d", i, seq, entry.Metadata.Sequence)
		}
		seq = entry.Metadata.Sequence
	}
	// Now check it updates if we set the sequence correctly.
	entry := storeData(t, []byte("first seq version"), fileName)
	entry.Metadata.Sequence = seq
	err, _ := context.Directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = context.Directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup file: %v", err)
	}
	if entry.Metadata.Sequence != seq+1 {
		t.Fatalf("wrong sequence: expected %d got %d", seq+1, entry.Metadata.Sequence)
	}
	// Now check it fails if we don't.
	entry = storeData(t, []byte("second seq version"), fileName)
	entry.Metadata.Sequence = seq
	err, _ = context.Directory.Put(entry)
	if err == nil {
		t.Fatal("expected error, got none")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "sequence mismatch") {
		t.Fatalf("expected sequence error, got %v", err)
	}
}

func TestRootDirectorySequencing(t *testing.T) {
	const (
		user     = "user7@google.com"
		fileName = user + "/file"
	)
	setup(user)
	// Validate sequence increases after write.
	seq := int64(-1)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, []byte(text), fileName)
		err, _ := context.Directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err = context.Directory.Lookup(user)
		if err != nil {
			t.Fatalf("lookup dir %d: %v", i, err)
		}
		if entry.Metadata.Sequence <= seq {
			t.Fatalf("sequence on dir %d did not increase: old seq %d; new seq %d", i, seq, entry.Metadata.Sequence)
		}
		seq = entry.Metadata.Sequence
	}
}

func init() {
	flag.StringVar(&useGCPStore, "use_gcp_store", "", "leave empty to use an in-process Store, or set to the URL of the GCP store (e.g. 'http://localhost:8080')")
}
