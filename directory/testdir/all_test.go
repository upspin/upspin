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

func packData(t *testing.T, data []byte, name upspin.PathName) ([]byte, upspin.PackData) {
	packer := pack.Lookup(context.Packing)
	if packer == nil {
		t.Fatalf("Packer is nil for packing %d", context.Packing)
	}

	meta := &upspin.Metadata{}

	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(context, data, meta, name)
	if cipherLen < 0 {
		t.Fatalf("PackLen failed for %v", name)
	}
	// TODO: Some packers don't update the meta in PackLen, but some do. If not done, update it now.
	if len(meta.PackData) == 0 {
		meta.PackData = []byte{byte(context.Packing)}
	}
	cipher := make([]byte, cipherLen)
	n, err := packer.Pack(context, cipher, data, meta, name)
	if err != nil {
		t.Fatal(err)
	}
	return cipher[:n], meta.PackData
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

	data, packdata := packData(t, []byte(text), fileName)
	loc, err := context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
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
		data, packdata := packData(t, []byte(text), fileName)
		loc, err := context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
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
		data, packdata := packData(t, []byte(text), fileName)
		h, err := context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
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
	data, packdata := packData(t, []byte(text), fileName)
	_, err = context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
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
	data, err = unpackBlob(context, cipher, fileName, &entry.Metadata)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	data, packdata = packData(t, []byte(text), fileName)
	_, err = context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
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
		data, packdata := packData(t, []byte(name), name)
		_, err := context.Directory.Put(name, data, packdata, nil) // TODO: Options
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

func TestSequenceIncreaseOnWrite(t *testing.T) {
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
		data, packdata := packData(t, []byte(text), fileName)
		_, err := context.Directory.Put(fileName, data, packdata, nil) // TODO: Options
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err := context.Directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup file %d: %v", i, err)
		}
		if entry == nil {
			t.Fatalf("lookup file %d: entry is nil", i)
		}
		if entry.Metadata.Sequence <= seq {
			t.Fatalf("sequence file %d did not increase: old seq %d; new seq %d", i, seq, entry.Metadata.Sequence)
		}
		seq = entry.Metadata.Sequence
	}
}

func init() {
	flag.StringVar(&useGCPStore, "use_gcp_store", "", "leave empty to use an in-process Store, or set to the URL of the GCP store (e.g. 'http://localhost:8080')")
}
