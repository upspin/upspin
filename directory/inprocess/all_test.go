// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

// This test uses an in-process Store service for the underlying
// storage. To run this test against a GCP Store, start a GCP store
// locally and run this test with flag
// -use_gcp_store=http://localhost:8080. It may take up to a minute
// to run.

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"upspin.io/bind"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user/inprocess"

	_ "upspin.io/pack/debug"
	_ "upspin.io/store/inprocess"
)

var (
	userNumber int32 // Updated atomically
)

func nextUser() upspin.UserName {
	atomic.AddInt32(&userNumber, 1)
	return upspin.UserName(fmt.Sprintf("user%d@google.com", userNumber))
}

func newContextAndServices(name upspin.UserName) (context *upspin.Context, user upspin.User, dir upspin.Directory, store upspin.Store) {
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	// TODO: This bootstrapping is fragile and will break. It depends on the order of setup.
	context = &upspin.Context{
		UserName:  name,
		Packing:   upspin.DebugPack,
		User:      endpoint,
		Directory: endpoint,
		Store:     endpoint,
	}
	var err error
	user, err = bind.User(context, endpoint)
	if err != nil {
		panic(err)
	}
	store, err = bind.Store(context, endpoint)
	if err != nil {
		panic(err)
	}
	dir, err = bind.Directory(context, endpoint)
	if err != nil {
		panic(err)
	}
	return
}

func setup() (*upspin.Context, upspin.User, upspin.Directory, upspin.Store) {
	context, user, dir, store := newContextAndServices(nextUser())
	err := user.(*inprocess.Service).Install(context.UserName, dir)
	if err != nil {
		panic(err)
	}
	key := upspin.PublicKey(fmt.Sprintf("key for %s", context.UserName))
	user.(*inprocess.Service).SetPublicKeys(context.UserName, []upspin.PublicKey{key})
	return context, user, dir, store
}

func packData(t *testing.T, context *upspin.Context, data []byte, entry *upspin.DirEntry, packing upspin.Packing) ([]byte, upspin.Packdata) {
	packer := pack.Lookup(packing)
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
	return cipher[:n], entry.Metadata.Packdata
}

func storeData(t *testing.T, context *upspin.Context, data []byte, name upspin.PathName) *upspin.DirEntry {
	return storeDataHelper(t, context, data, name, context.Packing)
}

func storePlainData(t *testing.T, context *upspin.Context, data []byte, name upspin.PathName) *upspin.DirEntry {
	return storeDataHelper(t, context, data, name, upspin.PlainPack)
}

func storeDataHelper(t *testing.T, context *upspin.Context, data []byte, name upspin.PathName, packing upspin.Packing) *upspin.DirEntry {
	if path.Clean(name) != name {
		t.Fatalf("%q is not a clean path name", name)
	}
	entry := &upspin.DirEntry{
		Name: name,
		Metadata: upspin.Metadata{
			Attr:     upspin.AttrNone,
			Size:     uint64(len(data)),
			Time:     upspin.Now(),
			Packdata: []byte{byte(packing)},
		},
	}
	cipher, packdata := packData(t, context, data, entry, packing)
	store, err := bind.Store(context, context.Store)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(cipher)
	if err != nil {
		t.Fatal(err)
	}
	entry.Location = upspin.Location{
		Endpoint:  context.Store,
		Reference: ref,
	}
	entry.Metadata.Packdata = packdata
	return entry
}

func TestPutTopLevelFileUsingDirectory(t *testing.T) {
	context, _, directory, store := setup()
	user := context.UserName
	root := upspin.PathName(user + "/")
	fileName := root + "file"
	const text = "hello sailor"

	entry1 := storeData(t, context, []byte(text), fileName)
	err := directory.Put(entry1)
	if err != nil {
		t.Fatal("put file:", err)
	}

	// Test that Lookup returns the same location.
	entry2, err := directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup %s: %s", fileName, err)
	}
	if entry1.Location != entry2.Location {
		t.Errorf("Lookup's location does not match Put's location:\t%v\n\t%v", entry1.Location, entry2.Location)
	}

	// Fetch the data back and inspect it.
	ciphertext, locs, err := store.Get(entry1.Location.Reference)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	if locs != nil {
		ciphertext, _, err = store.Get(locs[0].Reference)
		if err != nil {
			t.Fatal("get redirected blob:", err)
		}
	}
	clear, err := unpackBlob(context, "Put", ciphertext, entry1)
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
	context, _, directory, store := setup()
	user := context.UserName
	// Create a hundred files.
	locs := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		entry := storeData(t, context, []byte(text), fileName)
		err := directory.Put(entry)
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
		ciphertext, newLocs, err := store.Get(locs[j].Reference)
		if err != nil {
			t.Fatalf("%q: get blob: %v, ref: %v", fileName, err, locs[j].Reference)
		}
		if newLocs != nil {
			ciphertext, _, err = store.Get(newLocs[0].Reference)
			if err != nil {
				t.Fatalf("%q: get redirected blob: %v", fileName, err)
			}
		}
		entry, err := directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		clear, err := unpackBlob(context, "Lookup", ciphertext, entry)
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
	context, _, directory, store := setup()
	user := context.UserName
	// Create a hundred files.
	href := make([]upspin.Location, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := upspin.PathName(fmt.Sprintf("%s/file.%d", user, i))
		entry := storeData(t, context, []byte(text), fileName)
		err := directory.Put(entry)
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
		entry, err := directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("#%d: %q: lookup file: %v", i, fileName, err)
		}
		cipher, locs, err := store.Get(entry.Location.Reference)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		if locs != nil {
			cipher, _, err = store.Get(locs[0].Reference)
			if err != nil {
				t.Fatalf("%q: get redirected file: %v", fileName, err)
			}
		}
		entry, err = directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup %s: %s", fileName, err)
		}
		data, err := unpackBlob(context, "Lookup", cipher, entry)
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
	context, _, directory, store := setup()
	user := context.UserName
	_, err := directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf", user)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = directory.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot", user)))
	if err != nil {
		t.Fatal(err)
	}
	fileName := upspin.PathName(fmt.Sprintf("%s/foo/bar/asdf/zot/file", user))
	text := "hello world"
	entry := storeData(t, context, []byte(text), fileName)
	err = directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: lookup file: %v", fileName, err)
	}
	cipher, locs, err := store.Get(entry.Location.Reference)
	if err != nil {
		t.Fatalf("%q: get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = store.Get(locs[0].Reference)
		if err != nil {
			t.Fatalf("%q: get redirected file: %v", fileName, err)
		}
	}
	data, err := unpackBlob(context, "Get", cipher, entry)
	if err != nil {
		t.Fatalf("%q: unpack file: %v", fileName, err)
	}
	str := string(data)
	if str != text {
		t.Fatalf("expected %q; got %q", text, str)
	}
	// Now overwrite it.
	text = "goodnight mother"
	entry = storeData(t, context, []byte(text), fileName)
	err = directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Read it back.
	entry, err = directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("%q: second lookup file: %v", fileName, err)
	}
	cipher, locs, err = store.Get(entry.Location.Reference)
	if err != nil {
		t.Fatalf("%q: second get file: %v", fileName, err)
	}
	if locs != nil {
		cipher, _, err = store.Get(locs[0].Reference)
		if err != nil {
			t.Fatalf("%q: second get redirected file: %v", fileName, err)
		}
	}
	data, err = unpackBlob(context, "Get", cipher, entry)
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
	context, _, directory, _ := setup()
	user := context.UserName
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
		_, err := directory.MakeDirectory(name)
		if err != nil {
			t.Fatalf("make directory: %s: %v", name, err)
		}
	}
	for _, file := range files {
		name := upspin.PathName(fmt.Sprintf("%s/%s", user, file))
		entry := storeData(t, context, []byte(name), name)
		err := directory.Put(entry)
		if err != nil {
			t.Fatalf("make file: %s: %v", name, err)
		}
	}
	// Now do the test proper.
	for _, test := range globTests {
		name := fmt.Sprintf("%s/%s", user, test.pattern)
		entries, err := directory.Glob(name)
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
	context, _, directory, _ := setup()
	user := context.UserName
	fileName := upspin.PathName(user + "/file")
	// Validate sequence increases after write.
	seq := int64(-1)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, context, []byte(text), fileName)
		err := directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err = directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup file %d: %v", i, err)
		}
		if entry.Metadata.Sequence <= seq {
			t.Fatalf("sequence file %d did not increase: old seq %d; new seq %d", i, seq, entry.Metadata.Sequence)
		}
		seq = entry.Metadata.Sequence
	}
	// Now check it updates if we set the sequence correctly.
	entry := storeData(t, context, []byte("first seq version"), fileName)
	entry.Metadata.Sequence = seq
	err := directory.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = directory.Lookup(fileName)
	if err != nil {
		t.Fatalf("lookup file: %v", err)
	}
	if entry.Metadata.Sequence != seq+1 {
		t.Fatalf("wrong sequence: expected %d got %d", seq+1, entry.Metadata.Sequence)
	}
	// Now check it fails if we don't.
	entry = storeData(t, context, []byte("second seq version"), fileName)
	entry.Metadata.Sequence = seq
	err = directory.Put(entry)
	if err == nil {
		t.Fatal("expected error, got none")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "sequence mismatch") {
		t.Fatalf("expected sequence error, got %v", err)
	}
}

func TestRootDirectorySequencing(t *testing.T) {
	context, _, directory, _ := setup()
	user := context.UserName
	fileName := upspin.PathName(user + "/file")
	// Validate sequence increases after write.
	seq := int64(-1)
	for i := 0; i < 10; i++ {
		// Create a file.
		text := fmt.Sprintln("version", i)
		entry := storeData(t, context, []byte(text), fileName)
		err := directory.Put(entry)
		if err != nil {
			t.Fatalf("put file %d: %v", i, err)
		}
		entry, err = directory.Lookup(fileName)
		if err != nil {
			t.Fatalf("lookup dir %d: %v", i, err)
		}
		if entry.Metadata.Sequence <= seq {
			t.Fatalf("sequence on dir %d did not increase: old seq %d; new seq %d", i, seq, entry.Metadata.Sequence)
		}
		seq = entry.Metadata.Sequence
	}
}

func TestSeqNotExist(t *testing.T) {
	context, _, directory, _ := setup()
	user := context.UserName
	fileName := upspin.PathName(user + "/file")
	entry := storeData(t, context, []byte("hello"), fileName)
	// First write with SeqNotExist should succeed.
	entry.Metadata.Sequence = upspin.SeqNotExist
	err := directory.Put(entry)
	if err != nil {
		t.Fatalf("put file: %v", err)
	}
	// Second should fail.
	err = directory.Put(entry)
	if err == nil {
		t.Fatalf("put file succeeded; should have failed")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("put file expected 'already exists' error; got %v", err)
	}
}

func TestDelete(t *testing.T) {
	context, _, dir, _ := setup()
	user := context.UserName
	fileName := upspin.PathName(user + "/file")
	entry := storeData(t, context, []byte("hello"), fileName)
	err := dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	err = dir.Delete(fileName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err == nil {
		t.Fatal("file still exists after deletion")
	}
	// Another Delete should fail.
	err = dir.Delete(fileName)
	if err == nil {
		t.Fatal("second Delete succeeds")
	}
	const expect = "item does not exist"
	if !strings.Contains(err.Error(), expect) {
		t.Fatalf("second delete gives wrong error: %q; expected %q", err, expect)
	}
}

func TestDeleteDirectory(t *testing.T) {
	context, _, dir, _ := setup()
	user := context.UserName
	dirName := upspin.PathName(user + "/dir")
	fileName := dirName + "/file"
	_, err := dir.MakeDirectory(dirName)
	if err != nil {
		t.Fatal(err)
	}
	entry := storeData(t, context, []byte("hello"), fileName)
	err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	// File exists. First attempt to delete directory should fail.
	err = dir.Delete(dirName)
	if err == nil {
		t.Fatal("deleted non-empty directory")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("deleting non-empty directory succeeded with wrong error: %v", err)
	}
	// Now delete the file.
	err = dir.Delete(fileName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err == nil {
		t.Fatal("file still exists after deletion")
	}
	// Now try again to delete the directory.
	err = dir.Delete(dirName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(dirName)
	if err == nil {
		t.Fatal("directory still exists after deletion")
	}
}

func TestWhichAccess(t *testing.T) {
	context, _, dir, _ := setup()
	user := context.UserName
	dir1Name := upspin.PathName(user + "/dir1")
	dir2Name := dir1Name + "/dir2"
	fileName := dir2Name + "/file"
	accessFileName := dir1Name + "/Access"
	_, err := dir.MakeDirectory(dir1Name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.MakeDirectory(dir2Name)
	if err != nil {
		t.Fatal(err)
	}
	entry := storeData(t, context, []byte("hello"), fileName)
	err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dir.Lookup(fileName)
	if err != nil {
		t.Fatal(err)
	}
	// No Access file exists. Should get root.
	accessName, err := dir.WhichAccess(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if accessName != "" {
		t.Errorf("expected no Access file, got %q", accessName)
	}
	// Add an Access file to dir1.
	entry = storePlainData(t, context, []byte("r:*@google.com\n"), accessFileName)
	err = dir.Put(entry)
	if err != nil {
		t.Fatal(err)
	}
	accessName, err = dir.WhichAccess(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if accessName != accessFileName {
		t.Errorf("expected %q, got %q", accessFileName, accessName)
	}
}
