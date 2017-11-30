// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

// This test uses an in-process StoreServer for the underlying
// storage.
import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"
)

func init() {
	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, storeserver.New())
}

var (
	userNumber int32 // Updated atomically
)

func nextUser() upspin.UserName {
	atomic.AddInt32(&userNumber, 1)
	return upspin.UserName(fmt.Sprintf("user%d@google.com", userNumber))
}

func newConfigAndServices(name upspin.UserName) (cfg upspin.Config, key upspin.KeyServer, dir upspin.DirServer, store upspin.StoreServer) {
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	cfg = config.New()
	cfg = config.SetUserName(cfg, name)
	cfg = config.SetPacking(cfg, upspin.EEPack)
	cfg = config.SetKeyEndpoint(cfg, endpoint)
	cfg = config.SetStoreEndpoint(cfg, endpoint)
	cfg = config.SetDirEndpoint(cfg, endpoint)
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "user1")) // Always use user1's keys.
	if err != nil {
		panic(err)
	}
	cfg = config.SetFactotum(cfg, f)

	key, _ = bind.KeyServer(cfg, cfg.KeyEndpoint())
	store, _ = bind.StoreServer(cfg, cfg.KeyEndpoint())
	dir = New(cfg)
	return
}

func setup() (upspin.Config, upspin.DirServer) {
	userName := nextUser()
	config, key, dir, _ := newConfigAndServices(userName)
	user := &upspin.User{
		Name:      upspin.UserName(userName),
		Dirs:      []upspin.Endpoint{config.DirEndpoint()},
		Stores:    []upspin.Endpoint{config.StoreEndpoint()},
		PublicKey: config.Factotum().PublicKey(),
	}
	err := key.Put(user)
	if err != nil {
		panic(err)
	}
	_, err = makeDirectory(dir, upspin.PathName(userName))
	if err != nil {
		panic(err)
	}
	return config, dir
}

func storeData(t *testing.T, config upspin.Config, data []byte, name upspin.PathName) *upspin.DirEntry {
	return storeDataHelper(t, config, data, name, config.Packing())
}

func storePlainWithIntegrity(t *testing.T, config upspin.Config, data []byte, name upspin.PathName) *upspin.DirEntry {
	return storeDataHelper(t, config, data, name, upspin.EEIntegrityPack)
}

func storeDataHelper(t *testing.T, config upspin.Config, data []byte, name upspin.PathName, packing upspin.Packing) *upspin.DirEntry {
	if path.Clean(name) != name {
		t.Fatalf("%q is not a clean path name", name)
	}
	entry, err := newDirEntry(config, packing, name, data, upspin.AttrNone, "", upspin.SeqIgnore)
	if err != nil {
		t.Fatal(err)
	}
	// Our implementation stores a block for a zero-length file and newDirEntry sets that up,
	// but dirServer.put does not allow that, so clear out the blocks here for an empty file.
	if len(data) == 0 {
		entry.Blocks = nil
	}
	return entry
}

// readAll retrieves the data for the entry. It is a test-only version of Service.readAll.
func readAll(config upspin.Config, entry *upspin.DirEntry) ([]byte, error) {
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.Errorf("no packing %#x registered", entry.Packing)
	}
	u, err := packer.Unpack(config, entry)
	if err != nil {
		return nil, err
	}
	var data []byte
	for {
		block, ok := u.NextBlock()
		if !ok {
			break
		}
		store, err := bind.StoreServer(config, config.StoreEndpoint())
		if err != nil {
			return nil, err
		}
		ciphertext, _, locs, err := store.Get(block.Location.Reference)
		if err != nil {
			return nil, err
		}
		if locs != nil { // TODO
			return nil, errors.Str("dir/inprocess: redirection not implemented")
		}
		cleartext, err := u.Unpack(ciphertext)
		if err != nil {
			return nil, err
		}
		data = append(data, cleartext...)
	}
	return data, nil
}

// makeDirectory calls s.Put to create a directory.
func makeDirectory(dir upspin.DirServer, directoryName upspin.PathName) (*upspin.DirEntry, error) {
	parsed, err := path.Parse(directoryName)
	if err != nil {
		return nil, err
	}
	// Can't use newDirEntry as it adds a block.
	entry := &upspin.DirEntry{
		Name:       parsed.Path(),
		SignedName: parsed.Path(),
		Attr:       upspin.AttrDirectory,
	}
	return dir.Put(entry)
}

func TestPutTopLevelFileUsingDirectory(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
	root := upspin.PathName(user + "/")
	fileName := root + "file"
	const text = "hello sailor"

	entry1 := storeData(t, config, []byte(text), fileName)
	if len(entry1.Blocks) != 1 {
		t.Fatalf("internal error: %v: expected one block, found %d", fileName, len(entry1.Blocks))
	}
	_, err := directory.Put(entry1)
	if err != nil {
		t.Fatal("put file:", err)
	}

	// Test that Lookup returns the same location.
	entry2, err := directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup %s: %s", fileName, err)
	}
	if len(entry2.Blocks) != 1 {
		t.Fatalf("lookup %s: expected one block, found %d", fileName, len(entry2.Blocks))
	}
	if entry1.Blocks[0].Location != entry2.Blocks[0].Location {
		t.Errorf("Lookup's location does not match Put's location:\t%v\n\t%v", entry1.Blocks[0].Location, entry2.Blocks[0].Location)
	}

	// Fetch the data back and inspect it.
	clear, err := readAll(config, entry1)
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
	config, directory := setup()
	user := config.UserName()
	// Create a hundred files.
	locs := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := "X" + strings.Repeat(fmt.Sprint(i), i) // Need a non-empty file so we have a Location.
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		entry := storeData(t, config, []byte(text), fileName)
		_, err := directory.Put(entry)
		if err != nil {
			t.Fatal("put file:", err)
		}
		locs[i] = entry.Blocks[0].Location
	}
	// Read them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := "X" + strings.Repeat(fmt.Sprint(j), j)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		entry, err := directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		clear, err := readAll(config, entry)
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
	config, directory := setup()
	user := config.UserName()
	// Create a hundred files.
	href := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := "Y" + strings.Repeat(fmt.Sprint(i), i) // Need a non-empty file so we have a Location.
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		entry := storeData(t, config, []byte(text), fileName)
		_, err := directory.Put(entry)
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = entry.Blocks[0].Location
	}
	// Get them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := "Y" + strings.Repeat(fmt.Sprint(j), j)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		entry, err := directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		clear, err := readAll(config, entry)
		if err != nil {
			t.Fatalf("%q: unpack file: %v", fileName, err)
		}
		str := string(clear)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestCreateDirectoriesAndAFile(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
	dirName := upspin.PathName(fmt.Sprintf("%s/foo", user))
	entry, err := makeDirectory(directory, dirName)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("nil entry making directory")
	}
	if !entry.IsIncomplete() {
		t.Fatal("non-incomplete entry making directory")
	}
	_, err = makeDirectory(directory, upspin.PathName(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = makeDirectory(directory, upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = makeDirectory(directory, upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	entry = storeData(t, config, []byte(text), fileName)
	e, err := directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		t.Fatal("nil entry from Put")
	}
	// Read it back.
	entry, err = directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup: %v", fileName, err)
	}
	data, err := readAll(config, entry)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	entry = storeData(t, config, []byte(text), fileName)
	_, err = directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup: %v", fileName, err)
	}
	data, err = readAll(config, entry)
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
	err     error
}

var globTests = []globTest{
	{"", []string{""}, nil},
	{"*", []string{"ten", "twenty", "thirty"}, nil},
	{"ten/eleven/thirteen", []string{}, errors.E(errors.NotExist)},
	{"ten/twelve/thirteen", []string{"ten/twelve/thirteen"}, nil},
	{"ten/*", []string{"ten/twelve", "ten/eleven"}, nil},
	{"ten/twelve/*", []string{"ten/twelve/thirteen"}, nil},
	{"twenty/tw*", []string{"twenty/twentyone", "twenty/twentytwo"}, nil},
	{"*/*", []string{"ten/twelve", "ten/eleven", "twenty/twentyone", "twenty/twentytwo"}, nil},
}

func TestGlob(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
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
		_, err := makeDirectory(directory, name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, file))
		entry := storeData(t, config, []byte(name), name)
		_, err := directory.Put(entry)
		if err != nil {
			t.Fatalf("make file: %s: %v", name, err)
		}
	}
	// Now do the test proper.
	for i, test := range globTests {
		t.Logf("%d: pattern %q expect %q", i, test.pattern, test.files)
		name := fmt.Sprintf("%s/%s", user, test.pattern)
		entries, err := directory.Glob(name)
		if test.err != nil {
			if !errors.Match(test.err, err) {
				t.Errorf("%s: got error %q, want %q", name, err, test.err)
			}
			continue
		}
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

func TestGlobSyntaxError(t *testing.T) {
	config, directory := setup()
	// We need to create a file so the Glob test processes the whole pattern.
	user := config.UserName()
	root := upspin.PathName(user + "/")
	fileName := root + "file"
	entry := storeData(t, config, []byte("hello"), fileName)
	_, err := directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	expectErr := errors.E(errors.Op("dir/inprocess.Glob"), errors.Invalid)
	_, err = directory.Glob(string(config.UserName()) + "/[]")
	if !errors.Match(expectErr, err) {
		t.Fatalf("err = %v; expected %v", err, expectErr)
	}
}

func TestSequencing(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
	// New user root must start at SeqBase==1.
	entry, err := directory.Lookup(upspin.PathName(user))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Sequence != upspin.SeqBase {
		t.Errorf("%q has seq %d; expected %d", user, entry.Sequence, upspin.SeqBase)
	}
	fileName := upspin.PathName(user + "/file")
	// Validate sequence increases after write.
	seq := int64(upspin.SeqBase)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, config, []byte(text), fileName)
		retEntry, err := directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		if retEntry == nil {
			t.Fatalf("put file %d nil entry", i)
		}
		if !retEntry.IsIncomplete() {
			t.Fatalf("put file %q returns not-incomplete entry", fileName)
		}
		if retEntry.Sequence != seq+1 {
			t.Fatalf("sequence file %d did not get correct sequence: got seq %d; want seq %d", i, retEntry.Sequence, seq+1)
		}
		seq = retEntry.Sequence // Remember most recent sequence number.
	}
	// Now check it updates if we set the sequence correctly.
	// Ditto for the directory.
	entry, err = directory.Lookup(upspin.PathName(user))
	if err != nil {
		t.Fatalf("lookup root: %v", err)
	}
	dirSeq := entry.Sequence
	entry = storeData(t, config, []byte("first seq version"), fileName)
	entry.Sequence = seq
	retEntry, err := directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	if retEntry == nil {
		t.Fatal("nil entry returned from Put")
	}
	if retEntry.Sequence != entry.Sequence+1 {
		t.Fatalf("wrong sequence for file: expected %d got %d", seq+1, retEntry.Sequence)
	}
	entry, err = directory.Lookup(upspin.PathName(user))
	if err != nil {
		t.Fatalf("lookup root: %v", err)
	}
	if entry.Sequence != dirSeq+1 {
		t.Fatalf("wrong sequence for directory: expected %d got %d", dirSeq+1, entry.Sequence)
	}
	// Now check it fails if we don't.
	entry = storeData(t, config, []byte("second seq version"), fileName)
	entry.Sequence = seq
	_, err = directory.Put(entry)
	if err == nil {
		t.Fatal("expected error, got none")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "sequence mismatch") {
		t.Fatalf("expected sequence error, got %v", err)
	}
}

// Verify that the sequence number of all the elements of a path are the
// same as the most recently modified file below.
func TestSequenceToRoot(t *testing.T) {
	config, dirServer := setup()
	user := config.UserName()
	// Make a 5-element dir plus name: user@example.com/dir0/dir1/dir2/dir3/dir4/file
	dirName := upspin.PathName(user)
	var seq int64
	var dir3Name upspin.PathName // Path to .../dir3.
	for i := 0; i < 5; i++ {
		dirName = path.Join(dirName, fmt.Sprintf("dir%d", i))
		_, err := makeDirectory(dirServer, dirName)
		if err != nil {
			t.Fatal(err)
		}
		newSeq := consistentSeq(t, dirServer, dirName)
		if seq > 0 && newSeq != seq+1 {
			t.Fatalf("makeDirectory(%q) got seq %d; expected %d", dirName, newSeq, seq)
		}
		newSeq = seq
		if i == 3 {
			dir3Name = dirName
		}
	}
	fileName := path.Join(dirName, "file")
	entry := storeData(t, config, []byte("data"), fileName)
	_, err := dirServer.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	fileSeq := consistentSeq(t, dirServer, fileName)

	// Create intermediate file and verify state.
	file3Name := path.Join(dir3Name, "file3")
	entry = storeData(t, config, []byte("data"), file3Name)
	_, err = dirServer.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	file3Seq := consistentSeq(t, dirServer, file3Name)
	if file3Seq != fileSeq+1 {
		t.Errorf("%q has seq %d; expected %d", file3Name, file3Seq, fileSeq+1)
	}
	// fileName should still have same sequence.
	entry, err = dirServer.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Sequence != fileSeq {
		t.Errorf("%q has seq %d; expected %d", fileName, entry.Sequence, fileSeq)
	}
}

// consistentSeq is a test helper that verifies that all the elements of the named
// file, including the root, have the same sequence number. It returns that
// sequence number.
func consistentSeq(t *testing.T, dir upspin.DirServer, name upspin.PathName) int64 {
	t.Helper()
	parsed, err := path.Parse(name)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	// Loop goes one extra time (<=) so we visit root plus the elems.
	for i := 0; i <= parsed.NElem(); i++ {
		entry, err := dir.Lookup(parsed.First(i).Path())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			seq = entry.Sequence // Remember seq for root.
		}
		if entry.Sequence != seq {
			t.Errorf("path %q has seq %d; should be %d", entry.Name, entry.Sequence, seq)
		}
	}
	return seq
}

func TestRootDirectorySequencing(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
	fileName := upspin.PathName(user + "/file")
	// Validate sequence increases after write.
	seq := int64(-1)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, config, []byte(text), fileName)
		_, err := directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err = directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup dir %d: %v", i, err)
		}
		if entry.Sequence <= seq {
			t.Fatalf("sequence on dir %d did not increase: old seq %d; new seq %d", i, seq, entry.Sequence)
		}
		seq = entry.Sequence
	}
}

func TestSeqNotExist(t *testing.T) {
	config, directory := setup()
	user := config.UserName()
	fileName := upspin.PathName(user + "/file")
	entry := storeData(t, config, []byte("hello"), fileName)
	// First write with SeqNotExist should succeed.
	entry.Sequence = upspin.SeqNotExist
	_, err := directory.Put(entry)
	if err != nil {
		t.Fatalf("put file: %v", err)
	}
	// Second should fail.
	_, err = directory.Put(entry)
	if err == nil {
		t.Fatalf("put file succeeded; should have failed")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("put file expected 'already exists' error; got %v", err)
	}
}

func TestDelete(t *testing.T) {
	config, dir := setup()
	user := config.UserName()
	fileName := upspin.PathName(user + "/file")
	entry := storeData(t, config, []byte("hello"), fileName)
	_, err := dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Delete(fileName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err == nil {
		t.Fatal("file still exists after deletion")
	}
	// Another Delete should fail.
	_, err = dir.Delete(fileName)
	if err == nil {
		t.Fatal("second Delete succeeds")
	}
	const expect = "item does not exist"
	if !strings.Contains(err.Error(), expect) {
		t.Fatalf("second delete gives wrong error: %q; expected %q", err, expect)
	}
}

func TestDeleteDirectory(t *testing.T) {
	config, dir := setup()
	user := config.UserName()
	dirName := upspin.PathName(user + "/dir")
	fileName := dirName + "/file"
	_, err := makeDirectory(dir, dirName)
	if err != nil {
		t.Fatal(err)
	}
	entry := storeData(t, config, []byte("hello"), fileName)
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	// File exists. First attempt to delete directory should fail.
	_, err = dir.Delete(dirName)
	if err == nil {
		t.Fatal("deleted non-empty directory")
	}
	if err == upspin.ErrFollowLink {
		t.Fatal("unexpected link")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("deleting non-empty directory succeeded with wrong error: %v", err)
	}
	// Now delete the file.
	_, err = dir.Delete(fileName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err == nil {
		t.Fatal("file still exists after deletion")
	}
	// Now try again to delete the directory.
	_, err = dir.Delete(dirName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(dirName)
	if err == nil {
		t.Fatal("directory still exists after deletion")
	}
}

func TestWhichAccess(t *testing.T) {
	config, dir := setup()
	user := config.UserName()
	dir1Name := upspin.PathName(user + "/dir1")
	dir2Name := dir1Name + "/dir2"
	fileName := dir2Name + "/file"
	accessFileName := dir1Name + "/Access"
	_, err := makeDirectory(dir, dir1Name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = makeDirectory(dir, dir2Name)
	if err != nil {
		t.Fatal(err)
	}
	entry := storeData(t, config, []byte("hello"), fileName)
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	// No Access file exists. Should get root.
	accessEntry, err := dir.WhichAccess(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if accessEntry != nil {
		t.Errorf("expected no Access file, got %q", accessEntry.Name)
	}
	// Add an Access file to dir1.
	entry = storePlainWithIntegrity(t, config, []byte("r:*@google.com\n"), accessFileName)
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	accessEntry, err = dir.WhichAccess(dir1Name)
	if err != nil {
		t.Fatal(err)
	}
	if accessEntry == nil || accessEntry.Name != accessFileName {
		t.Errorf("expected %q, got %q", accessFileName, accessEntry.Name)
	}
	accessEntry, err = dir.WhichAccess(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if accessEntry == nil || accessEntry.Name != accessFileName {
		t.Errorf("expected %q, got %q", accessFileName, accessEntry.Name)
	}
	// Remove Access file from dir1.
	_, err = dir.Delete(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	// No access file exists (again). Should get root.
	accessEntry, err = dir.WhichAccess(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if accessEntry != nil {
		t.Errorf("expected no Access file, got %q", accessEntry.Name)
	}
}

func TestLinkToFile(t *testing.T) {
	config, dir := setup()
	user := config.UserName()
	dirName := upspin.PathName(user + "/dir")
	fileName := dirName + "/file"
	linkName := upspin.PathName(user + "/link")
	dirLinkName := upspin.PathName(user + "/dirlink")
	e, err := makeDirectory(dir, dirName)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		t.Fatal("nil entry from makeDirectory")
	}
	entry := storeData(t, config, []byte("hello"), fileName)
	e, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		t.Fatal("nil entry from Put")
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	// File exists. Now create a link to it in the root.
	linkEntry, err := newDirEntry(config, upspin.PlainPack, linkName, nil, upspin.AttrLink, fileName, upspin.SeqIgnore)
	if err != nil {
		t.Fatal(err)
	}
	e, err = dir.Put(linkEntry)
	if err != nil {
		t.Fatal(err)
	}
	linkEntry.Sequence = e.Sequence // Makes the checks for equality easier below.

	// Lookup the link, should get ErrFollow link with the right path.
	lookupEntry, err := dir.Lookup(linkName)
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v; expected %v", err, upspin.ErrFollowLink)
	}
	if !equal(linkEntry, lookupEntry) {
		t.Fatalf("lookup: expected\n\t%#v\ngot\n\t%#v", linkEntry, lookupEntry)
	}

	// Put through the link, should get ErrFollow link with the right path.
	putEntry, err := newDirEntry(config, upspin.PlainPack, linkName, []byte("hello"), upspin.AttrNone, "", upspin.SeqIgnore)
	if err != nil {
		t.Fatal(err)
	}
	e, err = dir.Put(putEntry)
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v; expected %v", err, upspin.ErrFollowLink)
	}
	if !equal(e, linkEntry) {
		t.Fatalf("put: expected %#v\ngot\n%#v", linkEntry, e)
	}

	// Make a link to the directory.
	dirLinkEntry, err := newDirEntry(config, upspin.PlainPack, dirLinkName, nil, upspin.AttrLink, dirName, upspin.SeqIgnore)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Put(dirLinkEntry)
	if err != nil {
		t.Fatal(err)
	}
	// Try to make a directory through the link, should get ErrFollowLink.
	e, err = makeDirectory(dir, dirLinkName+"/subdir")
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v; expected %v", err, upspin.ErrFollowLink)
	}
	if e.Link != dirName {
		t.Fatalf("link = %q; expected %q", e.Link, dirName)
	}

	// Test Glob("*/*"). We should get ErrFollowLink due to the evaluation of dirlink/*.
	entries, err := dir.Glob(string(user + "/*/*"))
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v; expected %v", err, upspin.ErrFollowLink)
	}
	if !equalNames(t, user, entries, []upspin.PathName{"dir/file", "dirlink", "link"}) {
		t.Fatal(`wrong names from Glob("*")`)
	}

	// Test Glob("*"). It should not error out, but instead include the links.
	entries, err = dir.Glob(string(user + "/*"))
	if err != nil {
		t.Fatalf("err = %v; expected none", err)
	}
	if !equalNames(t, user, entries, []upspin.PathName{"dir", "dirlink", "link"}) {
		t.Fatal(`wrong names from Glob("*")`)
	}

	// Now try to delete the file link, should succeed but leave the original intact.
	_, err = dir.Delete(linkName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
}

func equalNames(t *testing.T, user upspin.UserName, entries []*upspin.DirEntry, expectNames []upspin.PathName) bool {
	if len(entries) != len(expectNames) {
		t.Errorf("got %d entries, expected %d", len(entries), len(expectNames))
		return false
	}
	// The results are known to be sorted.
	for i, name := range expectNames {
		got := entries[i].Name
		expect := upspin.PathName(user) + "/" + name
		if got != upspin.PathName(expect) {
			t.Errorf("%d: name = %q; want = %q", i, got, expect)
			return false
		}
	}
	return true
}

func TestWhichAccessLink(t *testing.T) {
	config, dir := setup()
	user := config.UserName()
	// This is more elaborate than we need, but it's clear.
	// We construct a tree with a private directory and a public one, with
	// suitable access controls. (We just one user; it's all we need.)
	// The test verifies that a link in the public directory to a private
	// is controlled by the private Access file.
	publicDirName := upspin.PathName(user + "/public")
	privateDirName := upspin.PathName(user + "/private")
	privateFileName := privateDirName + "/file"
	publicLinkName := publicDirName + "/link" // Will point to the _private_file
	privateAccessFileName := upspin.PathName(user + "/private/Access")
	publicAccessFileName := upspin.PathName(user + "/public/Access")
	_, err := makeDirectory(dir, publicDirName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = makeDirectory(dir, privateDirName)
	if err != nil {
		t.Fatal(err)
	}
	entry := storeData(t, config, []byte("hello"), privateFileName)
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(privateFileName)
	if err != nil {
		t.Fatal(err)
	}
	// Private file exists. Now create a link to it in the public directory.
	linkEntry, err := newDirEntry(config, upspin.PlainPack, publicLinkName, nil, upspin.AttrLink, privateFileName, upspin.SeqIgnore)
	if err != nil {
		t.Fatal(err)
	}
	e, err := dir.Put(linkEntry)
	if err != nil {
		t.Fatal(err)
	}
	linkEntry.Sequence = e.Sequence // For easier equality check below.

	// Lookup the link, should get ErrFollow link with the right path.
	lookupEntry, err := dir.Lookup(publicLinkName)
	if err != upspin.ErrFollowLink {
		t.Fatalf("err = %v; expected %v", err, upspin.ErrFollowLink)
	}
	if !equal(linkEntry, lookupEntry) {
		t.Fatalf("lookup: expected %#v\ngot\n%#v", linkEntry, lookupEntry)
	}
	// All is well. Now create two access files, a public one and a private one.
	// The contents don't really matter, since DirServer doesn't evaluate links, but be thorough.
	entry = storePlainWithIntegrity(t, config, []byte("\n"), privateAccessFileName) // TODO(ehg,r): why is empty a problem with integrity?
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	allRights := fmt.Sprintf("*:%s\n", user)
	entry = storePlainWithIntegrity(t, config, []byte(allRights), publicAccessFileName)
	_, err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// WhichAccess should not show the private Access file, but instead present the link.
	entry, err = dir.WhichAccess(publicLinkName)
	if err != upspin.ErrFollowLink {
		t.Fatal(err)
	}
	if entry.Link != privateFileName {
		t.Fatalf("got %q for link; expected %q", entry.Link, privateFileName)
	}
}

// reflect.DeepEqual is too fussy, worrying about nil vs. empty. This is a lazy way to
// compare their equivalence.
func equal(d0, d1 *upspin.DirEntry) bool {
	b0, err := d0.Marshal()
	if err != nil {
		panic(err)
	}
	b1, err := d1.Marshal()
	if err != nil {
		panic(err)
	}
	return bytes.Equal(b0, b1)
}
