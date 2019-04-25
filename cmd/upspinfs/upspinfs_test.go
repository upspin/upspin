// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

// +build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	rtdebug "runtime/debug"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"bazil.org/fuse"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	dirserver "upspin.io/dir/inprocess"
	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"
)

var testConfig struct {
	mountpoint string
	cacheDir   string
	root       string
	user       string
	cfg        upspin.Config
}

const (
	perm           = 0777
	maxBytes int64 = 1e8
)

// testSetup creates a temporary user config with inprocess services.
func testSetup(name string) (upspin.Config, error) {
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "user1")) // Always use user1's keys.
	if err != nil {
		return nil, err
	}

	cfg := config.New()
	cfg = config.SetUserName(cfg, upspin.UserName(name))
	cfg = config.SetPacking(cfg, upspin.EEPack)
	cfg = config.SetKeyEndpoint(cfg, endpoint)
	cfg = config.SetStoreEndpoint(cfg, endpoint)
	cfg = config.SetDirEndpoint(cfg, endpoint)
	cfg = config.SetFactotum(cfg, f)

	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
	bind.RegisterStoreServer(upspin.InProcess, storeserver.New())
	bind.RegisterDirServer(upspin.InProcess, dirserver.New(cfg))

	publicKey := upspin.PublicKey(fmt.Sprintf("key for %s", name))
	user := &upspin.User{
		Name:      upspin.UserName(name),
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: publicKey,
	}
	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		return nil, err
	}
	err = key.Put(user)
	return cfg, err
}

func mount() error {
	// Create a mountpoint. There are 4 possible mountpoints /tmp/upsinfstest[1-4].
	// This lets us set up some /etc/fstab entries on Linux for the tests and
	// avoid using sudo.
	var err error
	found := false
	for i := 1; i < 5; i++ {
		testConfig.mountpoint = fmt.Sprintf("/tmp/upspinfstest%d", i)
		if err = os.Mkdir(testConfig.mountpoint, 0777); err == nil {
			found = true
			break
		}
	}
	if !found {
		for i := 1; i < 5; i++ {
			// No free mountpoint found. Just pick one and hope we aren't
			// breaking another test.
			testConfig.mountpoint = fmt.Sprintf("/tmp/upspinfstest%d", i)
			fuse.Unmount(testConfig.mountpoint)
			os.RemoveAll(testConfig.mountpoint)
			if err = os.Mkdir(testConfig.mountpoint, 0777); err == nil {
				found = true
				break
			}
		}
	}
	if !found {
		return err
	}

	// Set up a user config.
	testConfig.user = "tester@google.com"
	cfg, err := testSetup(testConfig.user)
	if err != nil {
		return err
	}
	testConfig.cfg = cfg

	// A directory for cache files.
	testConfig.cacheDir, err = ioutil.TempDir("", "upspincache")
	if err != nil {
		return err
	}

	// Mount the file system. It will be served in a separate go routine.
	log.SetLevel("info")
	do(cfg, testConfig.mountpoint, testConfig.cacheDir, maxBytes, false)

	// Create the user root, all tests will need it.
	testConfig.root = filepath.Join(testConfig.mountpoint, testConfig.user)
	return os.Mkdir(testConfig.root, 0777)
}

func cleanup() {
	fuse.Unmount(testConfig.mountpoint)
	os.RemoveAll(testConfig.mountpoint)
	os.RemoveAll(testConfig.cacheDir)
}

func TestMain(m *testing.M) {
	if os.Getenv("TRAVIS") == "true" {
		// TravisCI doesn't support FUSE filesystems.
		fmt.Fprintln(os.Stderr, "Skipping upspinfs tests on TravisCI.")
		os.Exit(0)
	}
	if err := mount(); err != nil {
		fmt.Fprintf(os.Stderr, "mount failed: %s", err)
		cleanup()
		os.Exit(1)
	}
	rv := m.Run()
	cleanup()
	os.Exit(rv)
}

func mkTestDir(t *testing.T, name string) string {
	testDir := filepath.Join(testConfig.root, name)
	if err := os.Mkdir(testDir, perm); err != nil {
		fatal(t, err)
	}
	return testDir
}

func randomBytes(t *testing.T, len int) []byte {
	buf := make([]byte, len)
	if _, err := rand.Read(buf); err != nil {
		fatal(t, err)
	}
	return buf
}

func writeFile(t *testing.T, fn string, buf []byte) *os.File {
	f, err := os.OpenFile(fn, os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		fatal(t, err)
	}
	n, err := f.Write(buf)
	if err != nil {
		f.Close()
		fatal(t, err)
	}
	if n != len(buf) {
		f.Close()
		fatalf(t, "%s: wrote %d bytes, expected %d", fn, n, len(buf))
	}
	return f
}

func truncateFile(t *testing.T, fn string, size int64) {
	if err := os.Truncate(fn, size); err != nil {
		fatal(t, err)
	}
}

func readAtAndCheckContentsOrDie(t *testing.T, file *os.File, offset int64, expected []byte) {
	err := readAtAndCheckContents(file, offset, expected)
	if err != nil {
		fatal(t, err)
	}
}

func readAtAndCheckContents(file *os.File, offset int64, expected []byte) error {
	buf := make([]byte, len(expected))
	n, err := file.ReadAt(buf, offset)
	if err != nil {
		return err
	}
	if n != len(expected) {
		return fmt.Errorf("read %d bytes, expected %d", n, len(expected))
	}
	for i := range expected {
		if expected[i] != buf[i] {
			return fmt.Errorf("error at byte %d: got %.2x expected %.2x", i, buf[i], expected[i])
		}
	}
	return nil
}

func openReadAndCheckContentsOrDie(t *testing.T, fn string, expected []byte) {
	err := openReadAndCheckContents(fn, expected)
	if err != nil {
		fatal(t, err)
	}
}

func openReadAndCheckContents(fn string, expected []byte) error {
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, len(expected)+10)
	n, err := f.Read(buf)
	if err != nil {
		return err
	}
	if n != len(expected) {
		return fmt.Errorf("%s: read %d bytes, expected %d", fn, n, len(expected))
	}
	for i := range expected {
		if expected[i] != buf[i] {
			return fmt.Errorf("%s: error at byte %d: got %.2x expected %.2x", fn, i, buf[i], expected[i])
		}
	}
	return nil
}

func mkFile(t *testing.T, fn string, buf []byte) {
	f := writeFile(t, fn, buf)
	if err := f.Close(); err != nil {
		fatal(t, err)
	}
}

func mkDir(t *testing.T, fn string) {
	if err := os.Mkdir(fn, perm); err != nil {
		fatal(t, err)
	}
}

func remove(t *testing.T, fn string) {
	if err := os.Remove(fn); err != nil {
		fatal(t, err)
	}
	notExist(t, fn, "removal")
}

func notExist(t *testing.T, fn, event string) {
	if _, err := os.Stat(fn); err == nil {
		fatalf(t, "%s: should not exist after %s", fn, event)
	}
}

// TestFile tests creating, writing, reading, and removing a file.
func TestFile(t *testing.T) {
	testDir := mkTestDir(t, "testfile")
	buf := randomBytes(t, 16*1024)

	// Create and write a file.
	fn := filepath.Join(testDir, "file")
	wf := writeFile(t, fn, buf)

	// Read before close.
	openReadAndCheckContentsOrDie(t, fn, buf)

	// Read after close.
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}
	openReadAndCheckContentsOrDie(t, fn, buf)

	// Test Rewriting part of the file.
	for i := 0; i < len(buf)/2; i++ {
		buf[i] = buf[i] ^ 0xff
	}
	wf = writeFile(t, fn, buf[:len(buf)/2])
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}
	openReadAndCheckContentsOrDie(t, fn, buf)
	remove(t, fn)

	if err := os.RemoveAll(testDir); err != nil {
		t.Fatal(err)
	}
}

// TestTruncateFile tests changing a file's size.
func TestTruncateFile(t *testing.T) {
	testDir := mkTestDir(t, "testtruncatefile")
	buf := randomBytes(t, 16*1024)

	// Create and write a file.
	fn := filepath.Join(testDir, "file")
	wf := writeFile(t, fn, buf)
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}

	// Truncate file and check.
	truncateFile(t, fn, 8*1024)
	openReadAndCheckContentsOrDie(t, fn, buf[:8*1024])

	// Extend file and check.
	truncateFile(t, fn, 16*1024)
	for i := 8 * 1024; i < 16*1024; i++ {
		buf[i] = 0
	}
	openReadAndCheckContentsOrDie(t, fn, buf)
	remove(t, fn)

	if err := os.RemoveAll(testDir); err != nil {
		t.Fatal(err)
	}
}

// TestExtendFile tests extending a preexisting file.
func TestExtendFile(t *testing.T) {
	testDir := mkTestDir(t, "testextendfile")
	// Written to the start of the file.
	buf1 := randomBytes(t, 16*1024)
	offset1 := 0
	// Written to overlap blocks 1 and 2.
	buf2 := randomBytes(t, 16*1024)
	offset2 := upspin.BlockSize - 3
	// A zero filled region between the two writes.
	buf3 := make([]byte, 16*1024)
	offset3 := upspin.BlockSize - 3 - len(buf3)

	// Create and write a file.
	fn := filepath.Join(testDir, "file")
	wf := writeFile(t, fn, buf1)
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and extend file traversing BlockSize.
	f, err := os.OpenFile(fn, os.O_RDWR, cachedFilePerms)
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.WriteAt(buf2, upspin.BlockSize-3)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf2) {
		t.Fatal("short write")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and check via upspinfs for consistency.
	f, err = os.OpenFile(fn, os.O_RDWR, cachedFilePerms)
	if err != nil {
		t.Fatal(err)
	}
	readAtAndCheckContentsOrDie(t, f, int64(offset1), buf1)
	readAtAndCheckContentsOrDie(t, f, int64(offset2), buf2)
	readAtAndCheckContentsOrDie(t, f, int64(offset3), buf3)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Now check via the upspin API to make sure it was really Put correctly.
	cl := client.New(testConfig.cfg)
	ufn := path.Join(upspin.PathName(testConfig.user), "testextendfile", "file")
	b, err := cl.Get(ufn)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(b[offset1:offset1+len(buf1)], buf1) != 0 {
		t.Fatal("mismatch 1")
	}
	if bytes.Compare(b[offset2:offset2+len(buf2)], buf2) != 0 {
		t.Fatal("mismatch 2")
	}
	if bytes.Compare(b[offset3:offset3+len(buf3)], buf3) != 0 {
		t.Fatal("mismatch 3")
	}
}

// TestSymlink tests creating, traversing, reading, and removing symnlinks.
func TestSymlink(t *testing.T) {
	testDir := mkTestDir(t, "testsymlinks")

	// The test will have the following directory structure:
	// dir/
	//   real1 - a real file
	//   sidelink - a link to dir/real
	//   downlink - a symlink to subdir/real
	//   subdir/
	//     real2 - a real file
	//     uplink - a link to dir/real
	//
	dir := filepath.Join(testDir, "dir")
	mkDir(t, dir)
	real1 := filepath.Join(dir, "real1")
	mkFile(t, real1, []byte(real1))
	subdir := filepath.Join(dir, "subdir")
	mkDir(t, subdir)
	real2 := filepath.Join(subdir, "real2")
	mkFile(t, real2, []byte(real2))

	// Test each link.
	testSymlink(t, filepath.Join(dir, "sidelink"), real1, "real1", []byte(real1))
	testSymlink(t, filepath.Join(dir, "downlink"), real2, "subdir/real2", []byte(real2))
	testSymlink(t, filepath.Join(subdir, "uplink"), real1, "../real1", []byte(real1))

	// Test a relative path that ..'s out and back in again.
	outIn := fmt.Sprintf("../../../../%s/testsymlinks/dir/real1", testConfig.user)
	testSymlink(t, filepath.Join(subdir, "updown"), outIn, "../real1", []byte(real2))

	// Test a path that leaves Upspin. It should fail.
	if err := os.Symlink("../../../../quux", filepath.Join(subdir, "wontwork")); err == nil {
		t.Fatalf("symlink out of upspin worked but should not have")
	}

	if err := os.RemoveAll(testDir); err != nil {
		fatal(t, err)
	}
}

// testSymlink creates and tests a symlink using both rooted and relative names.
func testSymlink(t *testing.T, link, rooted, relative string, contents []byte) {
	// Create and test using rooted name.
	if err := os.Symlink(rooted, link); err != nil {
		fatal(t, err)
	}
	val, err := os.Readlink(link)
	if err != nil {
		fatal(t, err)
	}
	if val != relative {
		fatalf(t, "%s: Readlink returned %s, expected %s:]", link, val, relative)
	}
	s, err := os.Lstat(link)
	if err != nil {
		fatal(t, err)
	}
	if s.Size() != int64(len(relative)) {
		fatalf(t, "%s(%v): Lstat returned size %v, expected %v, relative: %q, rooted: %q:]", link, len(link), s.Size(), len(relative), relative, rooted)
	}
	remove(t, link)

	// Create and test using relative name.
	if err := os.Symlink(relative, link); err != nil {
		fatal(t, err)
	}
	val, err = os.Readlink(link)
	if err != nil {
		fatal(t, err)
	}
	if val != relative {
		fatalf(t, "%s: Readlink returned %s, expected %s", link, val, relative)
	}
	s, err = os.Lstat(link)
	if err != nil {
		fatal(t, err)
	}
	if s.Size() != int64(len(relative)) {
		fatalf(t, "%s(%v): Lstat returned size %v, expected %v, relative: %q, rooted: %q:]", link, len(link), s.Size(), len(relative), relative, rooted)
	}
}

// TestRename tests renaming a file.
func TestRename(t *testing.T) {
	testDir := mkTestDir(t, "testrename")

	// Check that file is renamed and old name is no longer valid.
	original := filepath.Join(testDir, "original")
	newname := filepath.Join(testDir, "newname")
	mkFile(t, original, []byte(original))
	if err := os.Rename(original, newname); err != nil {
		t.Fatal(err)
	}
	openReadAndCheckContentsOrDie(t, newname, []byte(original))
	notExist(t, original, "rename")
	remove(t, newname)

	// Test on more time but with "newname" preexisting. It should be replaced.
	mkFile(t, original, []byte(original))
	mkFile(t, newname, []byte(newname))
	if err := os.Rename(original, newname); err != nil {
		t.Fatal(err)
	}
	openReadAndCheckContentsOrDie(t, newname, []byte(original))
	notExist(t, original, "rename")

	if err := os.RemoveAll(testDir); err != nil {
		t.Fatal(err)
	}
}

// TestAccess tests access control. This is not a rigorous right test, we just want
// to ensure that the access file is checked at file creation and open.
func TestAccess(t *testing.T) {
	testDir := mkTestDir(t, "testaccess")

	// First check that we can create a file.
	fn := filepath.Join(testDir, "newname")
	mkFile(t, fn, []byte(fn))

	// Now create an access fn allowing only read and list.
	access := filepath.Join(testDir, "Access")
	mkFile(t, access, []byte("r,l: "+testConfig.user+"\n"))

	// We should still be able to read.
	openReadAndCheckContentsOrDie(t, fn, []byte(fn))

	// Rewrite should fail.
	if _, err := os.OpenFile(fn, os.O_WRONLY, perm); err == nil {
		t.Fatalf("%s: can write after read only access", fn)
	}

	// Append should fail.
	if _, err := os.OpenFile(fn, os.O_WRONLY|os.O_APPEND, perm); err == nil {
		t.Fatalf("%s: can write after read only access", fn)
	}

	// Creating new files should fail.
	fn = fn + ".new"
	if _, err := os.OpenFile(fn, os.O_WRONLY|os.O_CREATE, perm); err == nil {
		t.Fatalf("%s: can write after read only access", fn)
	}

	// Removing Access should work.
	remove(t, access)

	if err := os.RemoveAll(testDir); err != nil {
		t.Fatal(err)
	}
}

// TestEventualConsistency tests upspinfs's ability to notice changes
// done behind its back. Because this is eventual concurrency, every
// test requires a loop waiting for the changes to appear.
func TestEventualConsistency(t *testing.T) {
	testDir := mkTestDir(t, "TestEventualConsistency")
	uTestDir := path.Join(upspin.PathName(testConfig.user), "TestEventualConsistency")
	cl := client.New(testConfig.cfg)

	// Create and write a file.
	buf := randomBytes(t, 128)
	fn := filepath.Join(testDir, "file")
	ufn := path.Join(uTestDir, "file")
	mkFile(t, fn, buf)

	// Rewrite the file via the Upspin API, that is, bypassing
	// FUSE and upspinfs. Test that reads via the host (via FUSE and upspinfs)
	// eventually see the contents change.
	buf2 := randomBytes(t, 128)
	if _, err := cl.Put(ufn, buf2); err != nil {
		fatal(t, err)
	}
	eventually(t, func() error { return openReadAndCheckContents(fn, buf2) }, 5*time.Second)

	// Create a new file via the Upspin API. Test that Reads via the host
	// correctly read it.
	fn = filepath.Join(testDir, "file2")
	ufn = path.Join(uTestDir, "file2")
	if _, err := cl.Put(ufn, buf2); err != nil {
		fatal(t, err)
	}
	eventually(t, func() error { return openReadAndCheckContents(fn, buf2) }, 5*time.Second)

	// Create a new file via the Upspin API. Test that Stat on the host sees it.
	fn = filepath.Join(testDir, "file3")
	ufn = path.Join(uTestDir, "file3")
	if _, err := cl.Put(ufn, buf2); err != nil {
		fatal(t, err)
	}
	eventually(t, func() error { _, err := os.Stat(fn); return err }, 5*time.Second)

	// Remove a file via the Upspin API. Test that Stat on the host eventually notices.
	if err := cl.Delete(ufn); err != nil {
		fatal(t, err)
	}
	f := func() error {
		_, err := os.Stat(fn)
		if err != nil {
			return nil
		}
		return errors.New("still there 1")
	}
	eventually(t, f, 5*time.Second)

	// Create a file via the Upspin API. Test Readdir on the host finds it.
	fn = filepath.Join(testDir, "file4")
	ufn = path.Join(uTestDir, "file4")
	if _, err := cl.Put(ufn, buf2); err != nil {
		fatal(t, err)
	}
	f = func() error {
		file, err := os.Open(testDir)
		if err != nil {
			return err
		}
		infos, err := file.Readdir(0)
		if err != nil {
			return nil
		}
		file.Close()
		for _, info := range infos {
			if info.Name() == "file4" {
				return nil
			}
		}
		return errors.New("not there")
	}
	eventually(t, f, 5*time.Second)

	// Delete a file via the Upspin API. Test that Readdir on the host notices.
	if err := cl.Delete(ufn); err != nil {
		fatal(t, err)
	}
	f = func() error {
		file, err := os.Open(testDir)
		if err != nil {
			return err
		}
		infos, err := file.Readdir(0)
		if err != nil {
			return nil
		}
		file.Close()
		found := false
		for _, info := range infos {
			if info.Name() == "file4" {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		return errors.New("still there 2")
	}
	eventually(t, f, 5*time.Second)
}

// TestDemandLoad tests loading multiple Block files.
func TestDemandLoad(t *testing.T) {
	testDir := mkTestDir(t, "TestDemandLoad")
	uTestDir := path.Join(upspin.PathName(testConfig.user), "TestDemandLoad")
	cl := client.New(testConfig.cfg)

	// Offsets into the file.
	first := int64(0)                      // offset of first block
	second := int64(upspin.BlockSize)      // offset of second block
	secondPlus := int64(second + 128*1024) // offset to data not cached by the kernel after reading the start of second
	third := int64(2 * upspin.BlockSize)   // offset of the third block

	// Create and write a file directly via the upspin API
	fn := filepath.Join(testDir, "file")
	ufn := path.Join(uTestDir, "file")
	buf1 := randomBytes(t, 128)
	buf2 := randomBytes(t, 128)
	buf2p := randomBytes(t, 128)
	buf3 := randomBytes(t, 128)
	buf := make([]byte, 4*upspin.BlockSize)
	copy(buf[0:128], buf1)
	copy(buf[second:second+128], buf2)
	copy(buf[secondPlus:secondPlus+128], buf2p)
	copy(buf[third:third+128], buf3)
	if _, err := cl.Put(ufn, buf); err != nil {
		fatal(t, err)
	}

	// Discount any blocks loaded up to this point.
	initial := atomic.LoadInt64(&cacheBlocksLoaded)

	// Read the file via the kernel FUSE file system. Check results and number of blocks demand loaded.
	file, err := os.Open(fn)
	if err != nil {
		fatal(t, err)
	}
	readAtAndCheckContentsOrDie(t, file, first, buf1)
	readAtAndCheckContentsOrDie(t, file, second, buf2)
	file.Close()
	if l := atomic.LoadInt64(&cacheBlocksLoaded); l != 2+initial {
		fatal(t, fmt.Sprintf("cacheBlocksLoaded: got %d expected %d", l, 2+initial))
	}

	file, err = os.Open(fn)
	if err != nil {
		fatal(t, err)
	}
	readAtAndCheckContentsOrDie(t, file, 0, buf1)           // already downloaded
	readAtAndCheckContentsOrDie(t, file, secondPlus, buf2p) // already downloaded
	readAtAndCheckContentsOrDie(t, file, third, buf3)       // not yet downloaded
	if l := atomic.LoadInt64(&cacheBlocksLoaded); l != 3+initial {
		fatal(t, fmt.Sprintf("cacheBlocksLoaded: got %d expected %d", l, 3+initial))
	}
	file.Close()
}

func TestCleanup(t *testing.T) {
	testDir := mkTestDir(t, "testcleanup")
	bufSize := int(maxBytes / 10)
	buf := randomBytes(t, bufSize)

	for i := 0; i < 20; i++ {
		fn := filepath.Join(testDir, strconv.Itoa(i))
		wf := writeFile(t, fn, buf)
		wf.Close()
		inUse := bytesUsed(t, testConfig.cacheDir)

		// Give it a little slack since we are doing this in parallel with the
		// other tests. The limit only covers closed files and, since this
		// executes in parallel with other tests, the total cache used could
		// be larger than the limit.
		if inUse > 5*maxBytes/4 {
			fatal(t, fmt.Errorf("cache too large %d > %d", inUse, maxBytes))
		}
	}
}

// bytesUsed does a recursive walk of the cache directories summing the bytes used.
func bytesUsed(t *testing.T, dir string) int64 {
	var sum int64
	fn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		sum += info.Size()
		return nil
	}
	err := filepath.Walk(dir, fn)
	if err != nil {
		fatal(t, err.Error())
	}
	return sum
}

func fatal(t *testing.T, args ...interface{}) {
	t.Log(fmt.Sprintln(args...))
	t.Log(string(rtdebug.Stack()))
	t.FailNow()
}

func fatalf(t *testing.T, format string, args ...interface{}) {
	t.Log(fmt.Sprintf(format, args...))
	t.Log(string(rtdebug.Stack()))
	t.FailNow()
}

// eventually attempts the function every 100 ms till period expires. If
// the function doesn't succeed by then, it fatals.
func eventually(t *testing.T, f func() error, d time.Duration) {
	end := time.Now().Add(d)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(end) {
			fatal(t, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
