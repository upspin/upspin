// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package test contains an integration test for all of Upspin.

package test

import (
	"fmt"
	"testing"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/key/usercache"
	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

const (
	contentsOfFile1     = "contents of file 1"
	contentsOfFile2     = "contents of file 2..."
	contentsOfFile3     = "===PDF PDF PDF=="
	genericFileContents = "contents"
	hasLocation         = true
	ownerName           = "test@upspin.io"
	snapshotUser        = "test+snapshot@upspin.io"
	readerName          = "test-friend@upspin.io"
)

var (
	errExist      = errors.E(errors.Exist)
	errNotExist   = errors.E(errors.NotExist)
	errPermission = errors.E(errors.Permission)
	errPrivate    = errors.E(errors.Private)

	setupTemplate = testenv.Setup{
		OwnerName: ownerName,
		UpBox:     true,
		Cache:     true,
		Cleanup:   cleanup,
	}
	readerConfig upspin.Config
)

func makeIntegrationTestTree(t *testing.T, r *testenv.Runner) {
	// TODO(adg): The tests in this file rely on this directory tree
	// existing at the root when they begin. We should probably consolidate
	// these tests into a single test, as they cannot be run in isolation
	// anyway.
	r.As(ownerName)
	r.MakeDirectory(ownerName + "/dir1")
	r.MakeDirectory(ownerName + "/dir2")
	r.Put(ownerName+"/dir1/file1.txt", contentsOfFile1)
	r.Put(ownerName+"/dir2/file2.txt", contentsOfFile2)
	r.Put(ownerName+"/dir2/file3.pdf", contentsOfFile3)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

func testNoReadersAllowed(t *testing.T, r *testenv.Runner) {
	fileName := upspin.PathName(ownerName + "/dir1/file1.txt")

	r.As(readerName)
	r.Get(fileName)
	if !r.Match(errPrivate) {
		t.Fatal(r.Diag())
	}

	// But the owner can still read it.
	r.As(ownerName)
	r.Get(fileName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, r.Data)
	}
}

func testAllowListAccess(t *testing.T, r *testenv.Runner) {
	r.As(ownerName)
	r.Put(ownerName+"/dir1/Access", "l:"+readerName)

	// Check that readerClient can list file1, but can't read and therefore the Location is zeroed out.
	file := ownerName + "/dir1/file1.txt"
	r.As(readerName)
	r.Glob(file)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if len(r.Entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(r.Entries))
	}
	checkDirEntry(t, r.Entries[0], ownerName+"/dir1/file1.txt", !hasLocation, 0)

	// Ensure we can't read the data.
	r.As(readerName)
	r.Get(upspin.PathName(file))
	if !r.Match(access.ErrPermissionDenied) {
		t.Fatal(r.Diag())
	}
}

func testAllowReadAccess(t *testing.T, r *testenv.Runner) {
	// Owner has no delete permission (assumption tested in testDelete).
	r.As(ownerName)
	r.Put(ownerName+"/dir1/Access",
		"l,r:"+readerName+"\nc,w,l,r:"+ownerName)
	// Put file back again so we force keys to be re-wrapped.
	r.Put(ownerName+"/dir1/file1.txt",
		contentsOfFile1)

	// Now try reading as the reader.
	r.As(readerName)
	r.Get(upspin.PathName(ownerName + "/dir1/file1.txt"))
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, r.Data)
	}
}

func testCreateAndOpen(t *testing.T, r *testenv.Runner) {
	filePath := upspin.PathName(path.Join(ownerName, "myotherfile.txt"))

	r.As(ownerName)
	r.Put(filePath, genericFileContents)
	r.Get(filePath)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != genericFileContents {
		t.Errorf("file content = %q, want %q", r.Data, genericFileContents)
	}
}

func testGlobWithLimitedAccess(t *testing.T, r *testenv.Runner) {
	dir1Pat := ownerName + "/dir1/*.txt"
	dir2Pat := ownerName + "/dir2/*.txt"
	bothPat := ownerName + "/dir*/*.txt"

	checkDirs := func(config, pat string, want int) {
		if r.Failed() {
			t.Fatalf("%v globbing %v: %v", config, pat, r.Diag())
		}
		got := len(r.Entries)
		if got == want {
			return
		}
		for _, d := range r.Entries {
			t.Log("got:", d.Name)
		}
		t.Fatalf("%v globbing %v saw %v dirs, want %v", config, pat, got, want)
	}

	// Owner sees both files.
	r.As(ownerName)
	r.Glob(bothPat)
	checkDirs("owner", bothPat, 2)
	checkDirEntry(t, r.Entries[0], ownerName+"/dir1/file1.txt", hasLocation, len(contentsOfFile1))
	checkDirEntry(t, r.Entries[1], ownerName+"/dir2/file2.txt", hasLocation, len(contentsOfFile2))

	// readerClient should be able to see /dir1/
	r.As(readerName)
	r.Glob(dir1Pat)
	checkDirs("reader", dir1Pat, 1)
	checkDirEntry(t, r.Entries[0], ownerName+"/dir1/file1.txt", hasLocation, len(contentsOfFile1))

	// but not /dir2/
	r.Glob(dir2Pat)
	if !r.Match(errPrivate) {
		t.Fatal(r.Diag())
	}

	// Without list access to the root, the reader can't glob /dir*.
	r.Glob(bothPat)
	if !r.Match(errPrivate) {
		t.Fatal(r.Diag())
	}

	// Give the reader list access to the root.
	r.As(ownerName)
	r.Put(ownerName+"/Access",
		"l:"+readerName+"\n*:"+ownerName)
	// But don't give any access to /dir2/.
	r.Put(ownerName+"/dir2/Access", "*:"+ownerName)

	// Then try globbing the root again.
	r.As(readerName)
	r.Glob(bothPat)
	checkDirs("reader after access", bothPat, 1)
	checkDirEntry(t, r.Entries[0], ownerName+"/dir1/file1.txt", hasLocation, len(contentsOfFile1))
}

func testGlobWithPattern(t *testing.T, r *testenv.Runner) {
	r.As(ownerName)
	for i := 0; i <= 10; i++ {
		r.MakeDirectory(upspin.PathName(fmt.Sprintf("%s/mydir%d", ownerName, i)))
	}
	r.Glob(ownerName + "/mydir[0-1]*")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if len(r.Entries) != 3 {
		t.Fatalf("Expected 3 paths, got %d", len(r.Entries))
	}
	if string(r.Entries[0].Name) != ownerName+"/mydir0" {
		t.Errorf("Expected mydir0, got %s", r.Entries[0].Name)
	}
	if string(r.Entries[1].Name) != ownerName+"/mydir1" {
		t.Errorf("Expected mydir1, got %s", r.Entries[1].Name)
	}
	if string(r.Entries[2].Name) != ownerName+"/mydir10" {
		t.Errorf("Expected mydir10, got %s", r.Entries[2].Name)
	}
}

func testDelete(t *testing.T, r *testenv.Runner) {
	pathName := upspin.PathName(ownerName + "/dir2/file3.pdf")

	r.As(ownerName)
	r.Delete(pathName)

	// Check it really deleted it (and is not being cached in memory).
	r.Get(pathName)
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}

	// But I can't delete files in dir1, since I lack permission.
	pathName = upspin.PathName(ownerName + "/dir1/file1.txt")
	r.Delete(pathName)
	if !r.Match(access.ErrPermissionDenied) {
		t.Fatal(r.Diag())
	}

	// But we can always remove the Access file.
	r.Delete(upspin.PathName(ownerName + "/dir1/Access"))

	// Now delete file1.txt
	r.Delete(pathName)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// testMetacharacters checks that we can handle files whose names
// contain Glob metacharacters.
func testMetacharacters(t *testing.T, r *testenv.Runner) {
	const (
		dir            = ownerName + "/foo[*]bar"
		subDir         = dir + "/inner?"
		subDirFile     = subDir + "/file[]"
		subDirFilePath = ownerName + "/foo???bar/in*/file??"
		contents       = "some text"
	)
	r.As(ownerName)
	r.MakeDirectory(dir)
	r.MakeDirectory(subDir)
	r.Put(subDirFile, contents)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	r.Get(subDirFile)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if r.Data != contents {
		t.Errorf("Expected contents %q, got %q", contents, r.Data)
	}

	// Use Glob to access file a different way to verify it's as we expect.
	r.Glob(subDirFilePath)
	checkDirEntry(t, r.Entries[0], subDirFile, hasLocation, len(contents))

	// Now clean up.
	r.Delete(subDirFile)
	r.Delete(subDir)
	r.Delete(dir)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

func testRootDeletion(t *testing.T, r *testenv.Runner) {
	r.As(readerName)

	// readerName does not have a root.
	r.Delete(readerName + "/")
	if !r.Match(errNotExist) {
		t.Fatal(r.Diag())
	}
	r.MakeDirectory(readerName + "/")
	if r.Failed() {
		t.Fatal(r.Diag())
	}

	// Now delete that root.
	r.Delete(readerName + "/")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// Creation can happen again.
	r.MakeDirectory(readerName + "/")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	// And we delete it again so this test can be re-run on a persistent
	// server.
	r.Delete(readerName + "/")
	if r.Failed() {
		t.Fatal(r.Diag())
	}
}

// integrationTests list all tests and their names. Order is important.
var integrationTests = []struct {
	name string
	fn   func(*testing.T, *testenv.Runner)
}{
	// These tests may be run independently.
	{"GetErrors", testGetErrors},
	{"GetLinkErrors", testGetLinkErrors},
	{"PutErrors", testPutErrors},
	{"PutLinkErrors", testPutLinkErrors},
	{"MakeDirectoryErrors", testMakeDirectoryErrors},
	{"MakeDirectoryLinkErrors", testMakeDirectoryLinkErrors},
	{"WhichAccess", testWhichAccess},
	{"WhichAccessErrors", testWhichAccessErrors},
	{"WhichAccessLinkErrors", testWhichAccessLinkErrors},
	{"GlobErrors", testGlobErrors},
	{"GlobLinkErrors", testGlobLinkErrors},
	{"SequenceNumbers", testSequenceNumbers},
	{"RootDeletion", testRootDeletion},
	{"ReadAccess", testReadAccess},
	{"GroupAccess", testGroupAccess},
	{"WriteReadAllAccessFile", testWriteReadAllAccessFile},
	{"CreateAccessFile", testCreateAccessFile},
	{"Metacharacters", testMetacharacters},

	{"Watch", testWatchCurrent},
	{"WatchErrors", testWatchErrors},
	{"WatchNonExistentFile", testWatchNonExistentFile},
	{"WatchNonExistentDir", testWatchNonExistentDir},
	{"WatchForbiddenFile", testWatchForbiddenFile},
	{"WatchSubtree", testWatchSubtree},
	{"WatchFile", testWatchFile},
	{"WatchNonExistentRoot", testWatchNonExistentRoot},

	{"CopyEntries", testCopyEntries},
	{"Snapshot", testSnapshot},
	{"DeleteErrors", testDeleteErrors},

	// Each of these tests depend on the output of the previous one.
	{"NoReadersAllowed", testNoReadersAllowed},
	{"AllowListAccess", testAllowListAccess},
	{"AllowReadAccess", testAllowReadAccess},
	{"CreateAndOpen", testCreateAndOpen},
	{"GlobWithLimitedAccess", testGlobWithLimitedAccess},
	{"GlobWithPattern", testGlobWithPattern},
	{"Delete", testDelete},
}

const remoteTestMessage = `
error: cannot find keys for remote test users.

These tests are designed to be run against the test.upspin.io cluster,
which is only accessible by the Upspin core team at Google.
See upspin.io/key/testdata/remote/README for details.

Run the test suite with -short to skip these tests.
`

func testSelectedOnePacking(t *testing.T, setup testenv.Setup) {
	usercache.ResetGlobal()

	env, err := testenv.New(&setup)
	if errors.Is(errors.NotExist, err) && setup.Kind == "remote" {
		t.Log(err)
		t.Fatal(remoteTestMessage)
	}
	if err != nil {
		t.Fatal(err)
	}

	if err := cleanup(env); err != nil {
		env.Exit()
		t.Fatal(err)
	}

	readerConfig, err = env.NewUser(readerName)
	if err != nil {
		env.Exit()
		t.Fatal(err)
	}
	snapshotConfig, err := env.NewUser(snapshotUser)
	if err != nil {
		env.Exit()
		t.Fatal(err)
	}

	r := testenv.NewRunner()
	r.AddUser(env.Config)
	r.AddUser(readerConfig)
	r.AddUser(snapshotConfig)

	// Build the test tree (for the tests in this file).
	makeIntegrationTestTree(t, r)

	// The ordering here is important as each test adds state to the tree.
	for _, test := range integrationTests {
		t.Run(test.name, func(t *testing.T) { test.fn(t, r) })
	}

	if err := r.FlushCache(); err != nil {
		t.Fatal(err)
	}
	if err := env.Exit(); err != nil {
		t.Fatal(err)
	}
}

func TestIntegration(t *testing.T) {
	type testConfig struct {
		kind    string // "inprocess", "server", or "remote".
		packing upspin.Packing
		cache   bool // Run a cacheserver for ownerName.
		always  bool // Always run this test, even with -test.short.
	}
	const noCache, cache = false, true
	testConfigs := []testConfig{
		{"inprocess", upspin.PlainPack, noCache, false},
		{"inprocess", upspin.EEIntegrityPack, noCache, false},
		{"inprocess", upspin.EEPack, noCache, true},

		{"server", upspin.PlainPack, noCache, false},
		{"server", upspin.EEIntegrityPack, noCache, false},
		{"server", upspin.EEPack, noCache, true},

		{"inprocess", upspin.EEPack, cache, true},
		{"server", upspin.EEPack, cache, true},

		{"remote", upspin.EEPack, noCache, false},
	}
	for _, config := range testConfigs {
		setup := setupTemplate
		setup.Kind = config.kind
		setup.Packing = config.packing
		setup.Cache = config.cache
		if config.kind == "remote" {
			setup.UpBox = false
		}
		name := fmt.Sprintf("kind=%v/packing=%v/cache=%t", config.kind, config.packing, config.cache)
		t.Run(name, func(t *testing.T) {
			if testing.Short() && !config.always {
				t.Skip("skipping because -test.short is set")
			}
			testSelectedOnePacking(t, setup)
		})
	}
}

// checkDirEntry verifies a dir entry against expectations. size == 0 for don't check.
func checkDirEntry(t *testing.T, dirEntry *upspin.DirEntry, name string, hasLocation bool, size int) {
	if dirEntry.Name != upspin.PathName(name) {
		t.Errorf("Expected name %s, got %s", name, dirEntry.Name)
	}
	if loc := locationOf(dirEntry); loc == (upspin.Location{}) {
		if hasLocation {
			t.Errorf("%s has no location, expected one", name)
		}
	} else {
		if !hasLocation {
			t.Errorf("%s has location %v, want none", name, loc)
		}
	}
	dSize, err := dirEntry.Size()
	if err != nil {
		t.Errorf("Size error: %s: %v", name, err)
	}
	if got, want := int(dSize), size; got != want {
		t.Errorf("%s has size %d, want %d", name, got, want)
	}
}

func locationOf(entry *upspin.DirEntry) upspin.Location {
	if len(entry.Blocks) == 0 {
		return upspin.Location{}
	}
	return entry.Blocks[0].Location
}

func cleanup(env *testenv.Env) error {
	dir, err := bind.DirServer(env.Config, env.Config.DirEndpoint())
	if err != nil {
		return err
	}
	return deleteAll(dir, upspin.PathName(env.Config.UserName()+"/"))
}

// deleteAll recursively deletes the directory named by path through the
// provided DirServer, first deleting path/Access and then path/*.
func deleteAll(dir upspin.DirServer, path upspin.PathName) error {
	if _, err := dir.Delete(path + "/Access"); err != nil {
		if !errors.Is(errors.NotExist, err) {
			return err
		}
	}
	entries, err := dir.Glob(string(path + "/*"))
	if err != nil && err != upspin.ErrFollowLink {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := deleteAll(dir, e.Name); err != nil {
				return err
			}
		}
		if _, err := dir.Delete(e.Name); err != nil {
			return err
		}
	}
	return nil
}
