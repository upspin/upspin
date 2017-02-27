// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package client

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	dirserver "upspin.io/dir/inprocess"
	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"

	"upspin.io/errors"
)

var baseCfg upspin.Config

func init() {
	inProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "user1")) // Always use user1's keys.
	if err != nil {
		panic("cannot initialize factotum: " + err.Error())
	}

	baseCfg = config.New()
	baseCfg = config.SetPacking(baseCfg, upspin.EEIntegrityPack)
	baseCfg = config.SetKeyEndpoint(baseCfg, inProcess)
	baseCfg = config.SetStoreEndpoint(baseCfg, inProcess)
	baseCfg = config.SetDirEndpoint(baseCfg, inProcess)
	baseCfg = config.SetFactotum(baseCfg, f)

	store, err := storeserver.New()
	if err != nil {
		panic(err)
	}
	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, store)
	bind.RegisterDirServer(upspin.InProcess, dirserver.New(baseCfg))
}

func checkTransport(s upspin.Service) {
	if s == nil {
		panic(fmt.Sprintf("nil service"))
	}
	if t := s.Endpoint().Transport; t != upspin.InProcess {
		panic(fmt.Sprintf("bad transport %v, want inprocess", t))
	}
}

func setup(userName upspin.UserName, publicKey upspin.PublicKey) upspin.Config {
	cfg := config.SetUserName(baseCfg, userName)
	key, _ := bind.KeyServer(cfg, cfg.KeyEndpoint())
	checkTransport(key)
	dir, _ := bind.DirServer(cfg, cfg.DirEndpoint())
	checkTransport(dir)
	if cfg.Factotum().PublicKey() == "" {
		panic("empty public key")
		// publicKey = upspin.PublicKey(fmt.Sprintf("key for %s", userName))
	}
	user := &upspin.User{
		Name:      upspin.UserName(userName),
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: cfg.Factotum().PublicKey(),
	}
	err := key.Put(user)
	if err != nil {
		panic(err)
	}
	name := upspin.PathName(userName) + "/"
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Attr:       upspin.AttrDirectory,
	}
	_, err = dir.Put(entry)
	if err != nil {
		panic(err)
	}
	return cfg
}

func TestPutGetTopLevelFile(t *testing.T) {
	const (
		user = "user1@google.com"
		root = user + "/"
	)
	client := New(setup(user, ""))
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	_, err := client.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	data, err := client.Get(fileName)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
}

const Max = 100 * 1000 // Must be > 100.

func setupFileIO(user upspin.UserName, fileName upspin.PathName, max int, t *testing.T) (upspin.Client, upspin.File, []byte) {
	client := New(setup(user, ""))
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}

	// Create a data set with each byte equal to its offset.
	data := make([]byte, max)
	for i := range data {
		data[i] = uint8(i)
	}
	return client, f, data
}

func TestFileSequentialAccess(t *testing.T) {
	const (
		user     = "user3@google.com"
		root     = user + "/"
		fileName = user + "/" + "file"
	)
	client, f, data := setupFileIO(user, fileName, Max, t)

	// Write the file in randomly sized chunks until it's full.
	for offset, length := 0, 0; offset < Max; offset += length {
		// Pick a random length.
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Write(data[offset : offset+length])
		if err != nil {
			t.Fatalf("Write(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Write length failed: offset %d expected %d got %d", offset, length, n)
		}
	}
	err := f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Now read it back with a similar scan.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, Max)
	for offset, length := 0, 0; offset < Max; offset += length {
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Read(buf[offset : offset+length])
		if err != nil {
			t.Fatalf("Read(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Read length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			if buf[i] != data[i] {
				t.Fatalf("Read at %d (%#x): expected %#.2x got %#.2x", i, i, data[i], buf[i])
			}
		}
	}
}

func TestFileRandomAccess(t *testing.T) {
	// Use a much smaller block size for this test, and not a multiple of
	// block cipher buffer length (512) to shake out block boundary issues.
	oldBlockSize := flags.BlockSize
	flags.BlockSize = 1023
	defer func() {
		flags.BlockSize = oldBlockSize
	}()

	const (
		user     = "user4@google.com"
		root     = user + "/"
		fileName = root + "file"
	)
	client, f, data := setupFileIO(user, fileName, Max, t)

	// Use WriteAt at random offsets and random sizes to create file.
	// Start with a map of bools (easy) saying the byte has been written.
	// Loop until its length is the file size, meaning every byte has been written.
	written := make(map[int]bool)
	for len(written) != Max {
		// Pick a random offset and length.
		offset := rand.Intn(Max)
		// Don't bother starting at a known location - speeds up the coverage.
		for written[offset] {
			offset = rand.Intn(Max)
		}
		length := rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.WriteAt(data[offset:offset+length], int64(offset))
		if err != nil {
			t.Fatalf("WriteAt(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("WriteAt length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			written[i] = true
		}
	}
	err := f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Read file back all at once, for simple verification.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := make([]byte, Max)
	n, err := f.Read(result)
	if err != nil {
		t.Fatal(err)
	}
	if n != Max {
		t.Fatalf("Read: expected %d got %d", Max, n)
	}
	if !bytes.Equal(data, result) {
		for i, c := range data {
			if result[i] != c {
				t.Fatalf("byte at offset %d should be %#.2x is %#.2x", i, c, result[i])
			}
		}
	}

	// Now use a similar algorithm to WriteAt but with ReadAt to check random access.
	read := make(map[int]bool)
	buf := make([]byte, Max)
	for len(read) != Max {
		// Pick a random offset and length.
		offset := rand.Intn(Max)
		// Don't bother starting at a known location - speeds up the coverage.
		for read[offset] {
			offset = rand.Intn(Max)
		}
		length := rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.ReadAt(buf[offset:offset+length], int64(offset))
		if err != nil {
			t.Fatalf("ReadAt(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("ReadAt length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			if buf[i] != data[i] {
				t.Fatalf("ReadAt at %d (%#x): expected %#.2x got %#.2x", i, i, data[i], buf[i])
			}
		}
		for i := offset; i < offset+length; i++ {
			read[i] = true
		}
	}
}

func TestFileZeroFill(t *testing.T) {
	const (
		user     = "zerofill@google.com"
		fileName = user + "/" + "file"
	)
	client, f, _ := setupFileIO(user, fileName, 0, t)
	// Create and write one byte 100 bytes out.
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}
	const N = 100
	n64, err := f.Seek(N, 0)
	if err != nil {
		t.Fatal("seek file:", err)
	}
	if n64 != N {
		t.Fatalf("seek file: expected %d got %d", N, n64)
	}
	n, err := f.Write([]byte{'x'})
	if err != nil {
		t.Fatal("write file:", err)
	}
	if n != 1 {
		t.Fatalf("write file: expected %d got %d", 1, n)
	}
	f.Close()
	// Read it back.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal("open file:", err)
	}
	defer f.Close()
	buf := make([]byte, 2*N) // Much more than was written.
	// Make it all non-zero.
	for i := range buf {
		buf[i] = 'y'
	}
	n, err = f.Read(buf)
	if err != nil {
		t.Fatal("read file:", err)
	}
	if n != N+1 {
		t.Fatalf("read file: expected %d got %d", N+1, n)
	}
	for i := 0; i < N; i++ {
		if buf[i] != 0 {
			t.Errorf("byte %d should be 0 is %#.2x", i, buf[i])
		}
	}
	if buf[N] != 'x' {
		t.Errorf("byte %d should be 'x' is %#.2x", N, buf[N])
	}
}

func globAndCheck(t *testing.T, client upspin.Client, pattern string, expect ...upspin.PathName) bool {
	entries, err := client.Glob(pattern)
	if err != nil {
		t.Errorf("Glob(%q): err = %v; expected none", pattern, err)
		return false
	}
	if len(entries) != len(expect) {
		t.Errorf("Glob(%q): got %d entries, expected %d:", pattern, len(entries), len(expect))
		for _, entry := range entries {
			t.Logf("\t%s", entry.Name)
		}
		return false
	}
	for i, p := range expect {
		if entries[i].Name != p {
			t.Errorf("Glob(%q): index %d: got %q; expected %q", pattern, i, p, entries[i].Name)
			return false
		}
	}
	return true
}

func TestGlob(t *testing.T) {
	const user = "multiuser@a.co"
	client := New(setup(user, ""))
	var err error

	for _, fno := range []int{0, 1, 7, 17} {
		fileName := fmt.Sprintf("%s/testfile%d.txt", user, fno)
		text := fmt.Sprintf("Contents of file %s", fileName)
		_, err = client.Put(upspin.PathName(fileName), []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
	}

	if !globAndCheck(t, client, "multiuser@a.co/testfile*.txt",
		"multiuser@a.co/testfile0.txt", "multiuser@a.co/testfile1.txt", "multiuser@a.co/testfile17.txt", "multiuser@a.co/testfile7.txt") {
		t.Error("glob failed")
	}
	if !globAndCheck(t, client, "multiuser@a.co/*7.txt",
		"multiuser@a.co/testfile17.txt", "multiuser@a.co/testfile7.txt") {
		t.Error("glob failed")
	}
	if !globAndCheck(t, client, "multiuser@a.co/*1*.txt",
		"multiuser@a.co/testfile1.txt", "multiuser@a.co/testfile17.txt") {
		t.Error("glob failed")
	}

}

func TestPutDuplicateAndRename(t *testing.T) {
	const user = "link@a.com"
	client := New(setup(user, ""))
	original := upspin.PathName(fmt.Sprintf("%s/original", user))
	text := "the rain in spain"
	if _, err := client.Put(original, []byte(text)); err != nil {
		t.Fatal("put file:", err)
	}

	// Duplicate: create a new name for the same reference.
	dup := upspin.PathName(fmt.Sprintf("%s/dup", user))
	entry, err := client.PutDuplicate(original, dup)
	if err != nil {
		t.Fatal("duplicate file:", err)
	}
	if entry == nil {
		t.Fatal("nil entry returned by PutDuplicate")
	}
	if entry.Name != dup {
		t.Fatal("duped directory entry has wrong name")
	}
	in, err := client.Get(original)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(in) != text {
		t.Fatal(fmt.Sprintf("contents of %q corrupted", original))
	}
	in, err = client.Get(dup)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(in) != text {
		t.Fatal(fmt.Sprintf("contents of %q and %q don't match", original, dup))
	}

	// Rename the new file.
	renamed := upspin.PathName(fmt.Sprintf("%s/renamed", user))
	if err := client.Rename(dup, renamed); err != nil {
		t.Fatal("link file:", err)
	}
	if _, err := client.Get(dup); err == nil {
		t.Fatal("renamed file still exists")
	}
	in, err = client.Get(renamed)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(in) != text {
		t.Fatal(fmt.Sprintf("contents of %q and %q don't match", renamed, original))
	}
}

func TestSimpleLinks(t *testing.T) {
	const (
		user     = "linker@google.com"
		root     = user + "/"
		dirName  = root + "/dir"
		fileName = dirName + "/file"
		linkName = root + "/link"
		text     = "hello sailor"
		linkText = "what a lovely day"
	)
	client := New(setup(user, ""))
	// Install and check file.
	_, err := client.MakeDirectory(dirName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
	// Make a link.
	entry, err := client.PutLink(fileName, linkName)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("empty entry from PutLink")
	}
	// Get through the link.
	data, err = client.Get(linkName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
	// Put through the link.
	_, err = client.Put(fileName, []byte(linkText))
	if err != nil {
		t.Fatal(err)
	}
	// Get through the file.
	data, err = client.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != linkText {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, linkText)
	}
	// Get through the link.
	data, err = client.Get(linkName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != linkText {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, linkText)
	}
	// Delete the link.
	err = client.Delete(linkName)
	if err != nil {
		t.Fatal(err)
	}
	// Get through the file, which should still be there.
	_, err = client.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGlobLinks(t *testing.T) {
	const (
		user     = "linkglobber@google.com"
		root     = user
		dirName  = root + "/dir"
		fileName = dirName + "/file"
		linkName = root + "/link" // Will point to dir.
		text     = "ignored"
	)
	client := New(setup(user, ""))
	_, err := client.MakeDirectory(dirName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Link to directory.
	_, err = client.PutLink(dirName, linkName)
	if err != nil {
		t.Fatal(err)
	}

	// There are two entries in the root, one a link.
	if !globAndCheck(t, client, root+"/*",
		"linkglobber@google.com/dir", "linkglobber@google.com/link") {
		t.Error("glob failed")
	}
	if !globAndCheck(t, client, dirName+"/*",
		"linkglobber@google.com/dir/file") {
		t.Error("glob failed")
	}
	log.SetLevel("debug")
	if !globAndCheck(t, client, linkName+"/*",
		"linkglobber@google.com/dir/file") {
		t.Error("glob failed")
	}
	log.SetLevel("info")
	// There is only one file, although there are two paths to it.
	if !globAndCheck(t, client, root+"/*/*",
		"linkglobber@google.com/dir/file") {
		t.Error("glob failed")
	}
}

func TestRejectBadAccessFile(t *testing.T) {
	const (
		user          = "bad@access.org"
		root          = user
		accessFile    = root + "/Access"
		accessContent = "all:*"
	)
	client := New(setup(user, ""))
	_, err := client.Put(accessFile, []byte(accessContent))
	expectedErr := errors.E(upspin.PathName(accessFile), errors.Invalid)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("error = %s, want = %s", err, expectedErr)
	}
}

func TestRejectBadGroupFile(t *testing.T) {
	const (
		user         = "bad@group.org"
		root         = user
		groupDir     = root + "/Group"
		groupFile    = groupDir + "/mygroup"
		groupContent = "foo@x, yo! ; whoo-hoo!"
	)
	client := New(setup(user, ""))
	_, err := client.MakeDirectory(groupDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Put(groupFile, []byte(groupContent))
	expectedErr := errors.E(upspin.PathName(groupFile), errors.Invalid)
	if !errors.Match(expectedErr, err) {
		t.Fatalf("error = %s, want = %s", err, expectedErr)
	}
}
