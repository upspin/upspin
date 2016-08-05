// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package test contains an integration test for all of Upspin.
// This particular integration test runs on GCP and as such we disable it
// from normal 'go test ./...' runs since it's too
// expensive. To run it, do 'go test -tags integration'
//
// Note: if this test fails with "directory already exists" it means a bad run of it left data behind.
// For now, the quickest way to recover is to manually erase everything under
// gs://upspin-test-dir/upspin-test@google.com and restart the test servers. This will do both:
//
// gsutil rm -R gs://upspin-test-dir/upspin-test@google.com
// cd <your_upspin_srcdir>/cmd/admin
// ./deploy-servers.sh -t -r directory
//
// None of this is needed if the tests complete normally.

// +build integration

package test

import (
	"fmt"
	"log"
	"strings"
	"testing"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/path"
	"upspin.io/test/testenv"
	"upspin.io/upspin"

	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/transports"
)

const (
	unauthorizedUser    = "sally@unauthorized.com"
	contentsOfFile1     = "contents of file 1"
	contentsOfFile2     = "contents of file 2..."
	contentsOfFile3     = "===PDF PDF PDF=="
	genericFileContents = "contents"
	dirAlreadyExists    = "directory already exists" // TODO: make this a global error in /upspin/errors.go?

	hasLocation = true
)

var (
	setupTemplate = testenv.Setup{
		Tree: testenv.Tree{
			testenv.E("/dir1/", ""),
			testenv.E("/dir2/", ""),
			testenv.E("/dir1/file1.txt", contentsOfFile1),
			testenv.E("/dir2/file2.txt", contentsOfFile2),
			testenv.E("/dir2/file3.pdf", contentsOfFile3),
		},
		OwnerName:                 ownerName,
		IgnoreExistingDirectories: false, // left-over Access files would be a problem.
		Cleanup:                   cleanup,
	}
	readerClient upspin.Client
)

func testNoReadersAllowed(t *testing.T, env *testenv.Env) {
	var err error
	fileName := upspin.PathName(ownerName + "/dir1/file1.txt")
	_, err = readerClient.Get(fileName)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error contains %q, got %q", access.ErrPermissionDenied, err)
	}
	// But the owner can still read it.
	data, err := env.Client.Get(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, data)
	}
}

func testAllowListAccess(t *testing.T, env *testenv.Env) {
	_, err := env.Client.Put(ownerName+"/dir1/Access", []byte("l:"+readerName))
	if err != nil {
		t.Fatal(err)
	}
	// Now check that readerClient can list file1, but can't read and therefore the Location is zeroed out.
	file := ownerName + "/dir1/file1.txt"
	dirs, err := readerClient.Glob(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownerName+"/dir1/file1.txt"), !hasLocation, 0)

	// Ensure we can't read the data.
	_, err = readerClient.Get(upspin.PathName(file))
	if err == nil {
		t.Errorf("Get(%q) succeeded, expected permission denied", file)
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Errorf("Get(%q) returned error %q, expected permission denied", file, err)
	}
}

func testAllowReadAccess(t *testing.T, env *testenv.Env) {
	// Owner has no delete permission (assumption tested in testDelete).
	_, err := env.Client.Put(ownerName+"/dir1/Access", []byte("l,r:"+readerName+"\nc,w,l,r:"+ownerName))
	if err != nil {
		t.Fatal(err)
	}
	data, err := readerClient.Get(upspin.PathName(ownerName + "/dir1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, data)
	}
}

func testCreateAndOpen(t *testing.T, env *testenv.Env) {
	filePath := upspin.PathName(path.Join(ownerName, "myotherfile.txt"))
	c := env.Client

	f, err := c.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.Write([]byte(genericFileContents))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(genericFileContents) {
		t.Fatalf("Expected to write %d bytes, got %d", len(genericFileContents), n)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err = c.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 30)
	n, err = f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(genericFileContents) {
		t.Fatalf("Expected to read %d bytes, got %d", len(genericFileContents), n)
	}
	buf = buf[:n]
	if string(buf) != genericFileContents {
		t.Errorf("Expected to read %q, got %q", genericFileContents, buf)
	}
}

func testGlobWithLimitedAccess(t *testing.T, env *testenv.Env) {
	dir1Pat := ownerName + "/dir1/*.txt"
	dir2Pat := ownerName + "/dir2/*.txt"
	bothPat := ownerName + "/dir*/*.txt"

	checkDirs := func(context, pat string, dirs []*upspin.DirEntry, want int) {
		got := len(dirs)
		if got == want {
			return
		}
		for _, d := range dirs {
			t.Log("got:", d.Name)
		}
		t.Fatalf("%v globbing %v saw %v dirs, want %v", context, pat, got, want)
	}

	// Owner sees both files.
	dirs, err := env.Client.Glob(bothPat)
	if err != nil {
		t.Fatal(err)
	}
	checkDirs("owner", bothPat, dirs, 2)
	checkDirEntry(t, dirs[0], upspin.PathName(ownerName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
	checkDirEntry(t, dirs[1], upspin.PathName(ownerName+"/dir2/file2.txt"), hasLocation, len(contentsOfFile2))

	// readerClient should be able to see /dir1/
	dirs, err = readerClient.Glob(dir1Pat)
	if err != nil {
		t.Fatal(err)
	}
	checkDirs("reader", dir1Pat, dirs, 1)
	checkDirEntry(t, dirs[0], upspin.PathName(ownerName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))

	// but not /dir2/
	dirs, err = readerClient.Glob(dir2Pat)
	if err != nil {
		t.Fatal(err)
	}
	checkDirs("reader", dir2Pat, dirs, 0)

	// Without list access to the root, the reader can't glob /dir*.
	dirs, err = readerClient.Glob(bothPat)
	if err != nil {
		t.Fatal(err)
	}
	checkDirs("reader", bothPat, dirs, 0)

	// Give the reader list access to the root.
	_, err = env.Client.Put(ownerName+"/Access", []byte("l:"+readerName+"\n*:"+ownerName))
	if err != nil {
		t.Fatal(err)
	}
	// But don't give any access to /dir2/.
	_, err = env.Client.Put(ownerName+"/dir2/Access", []byte("*:"+ownerName))
	if err != nil {
		t.Fatal(err)
	}
	// Then try globbing the root again.
	dirs, err = readerClient.Glob(bothPat)
	if err != nil {
		t.Fatal(err)
	}
	checkDirs("reader after access", bothPat, dirs, 1)
	checkDirEntry(t, dirs[0], upspin.PathName(ownerName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
}

func testGlobWithPattern(t *testing.T, env *testenv.Env) {
	c := env.Client

	for i := 0; i <= 10; i++ {
		dirPath := upspin.PathName(fmt.Sprintf("%s/mydir%d", ownerName, i))
		_, err := c.MakeDirectory(dirPath)
		if err != nil && !strings.Contains(err.Error(), dirAlreadyExists) {
			t.Fatal(err)
		}
	}
	dirEntries, err := c.Glob(ownerName + "/mydir[0-1]*")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirEntries) != 3 {
		t.Fatalf("Expected 3 paths, got %d", len(dirEntries))
	}
	if string(dirEntries[0].Name) != ownerName+"/mydir0" {
		t.Errorf("Expected mydir0, got %s", dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != ownerName+"/mydir1" {
		t.Errorf("Expected mydir1, got %s", dirEntries[1].Name)
	}
	if string(dirEntries[2].Name) != ownerName+"/mydir10" {
		t.Errorf("Expected mydir10, got %s", dirEntries[2].Name)
	}
}

func testDelete(t *testing.T, env *testenv.Env) {
	pathName := upspin.PathName(ownerName + "/dir2/file3.pdf")
	dir, err := bind.DirServer(env.Context, env.Context.DirEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	err = dir.Delete(pathName)
	// Check it really deleted it (and is not being cached in memory).
	_, err = env.Client.Get(pathName)
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if !strings.Contains(err.Error(), "not exist") {
		t.Errorf("Expected error contains not exist, got %s", err)
	}
	// But I can't delete files in dir1, since I lack permission.
	pathName = upspin.PathName(ownerName + "/dir1/file1.txt")
	err = dir.Delete(pathName)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error %s, got %s", access.ErrPermissionDenied, err)
	}
	// But we can always remove the Access file.
	accessPathName := upspin.PathName(ownerName + "/dir1/Access")
	err = dir.Delete(accessPathName)
	if err != nil {
		t.Fatal(err)
	}
	// Now delete file1.txt
	err = dir.Delete(pathName)
	if err != nil {
		t.Fatal(err)
	}
}

func testSharing(t *testing.T, env *testenv.Env) {
	const (
		sharedContent = "Hey man, whatup?"
	)
	var (
		sharedDir      = path.Join(ownerName, "mydir")
		sharedFilePath = path.Join(sharedDir, "sharedfile")
	)
	c := env.Client

	// Put an Access file where no one has access (this forces updating the parent dir with no access).
	accessFile := "r,c,w,d,l:" + ownerName // all rights to the owner.
	_, err := c.Put(path.Join(sharedDir, "Access"), []byte(accessFile))
	if err != nil {
		t.Fatal(err)
	}
	// Put a new file under a previously created dir.
	_, err = c.Put(sharedFilePath, []byte(sharedContent))
	if err != nil {
		t.Fatal(err)
	}
	// Use the other user to read the file and get told no.
	data, err := readerClient.Get(sharedFilePath)
	if err == nil {
		t.Fatal("Expected Get to fail, but it didn't")
	}

	// Put an Access file first, giving our friend read access. The owner retains all rights.
	accessFile = fmt.Sprintf("r: %s\nr,c,w,d,l:%s", readerName, ownerName)
	_, err = c.Put(path.Join(sharedDir, "Access"), []byte(accessFile))
	if err != nil {
		t.Fatal(err)
	}
	// Re-write file, so we wrap keys for our friend.
	_, err = c.Put(sharedFilePath, []byte(sharedContent))
	if err != nil {
		t.Fatal(err)
	}
	// Now become some other user again and verify that he has access now.
	data, err = readerClient.Get(sharedFilePath)
	// And this should not fail under any packing.
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sharedContent {
		t.Errorf("Expected %s, got %s", sharedContent, data)
	}
}

func testAllOnePacking(t *testing.T, setup testenv.Setup) {
	var readerKey testenv.KeyPair
	// Keys are needed with any packing type (even Plain) for auth purposes.
	setup.Keys = keyStore[setup.OwnerName][setup.KeyKind]
	readerKey = keyStore[readerName][setup.KeyKind]

	env, err := testenv.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	readerClient, _, err = env.NewUser(readerName, &readerKey)
	if err != nil {
		t.Fatal(err)
	}

	// The ordering here is important as each test adds state to the tree.
	for _, test := range []struct {
		name string
		fn   func(*testing.T, *testenv.Env)
	}{
		{"NoReadersAllowed", testNoReadersAllowed},
		{"AllowListAccess", testAllowListAccess},
		{"AllowReadAccess", testAllowReadAccess},
		{"CreateAndOpen", testCreateAndOpen},
		{"GlobWithLimitedAccess", testGlobWithLimitedAccess},
		{"GlobWithPattern", testGlobWithPattern},
		{"Delete", testDelete},
	} {
		t.Run(test.name, func(t *testing.T) { test.fn(t, env) })
	}

	err = env.Exit()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegration(t *testing.T) {
	for _, transport := range []upspin.Transport{upspin.InProcess, upspin.GCP} {
		t.Run(fmt.Sprintf("transport=%v", transport), func(t *testing.T) {
			setup := setupTemplate
			setup.Transport = transport
			for _, p := range []struct {
				packing upspin.Packing
				curve   string
			}{
				{packing: upspin.PlainPack, curve: "p256"},
				{packing: upspin.DebugPack, curve: "p256"},
				{packing: upspin.EEPack, curve: "p256"},
				//{packing: upspin.EEPack, curve: "p521"}, // TODO: figure out if and how to test p521.
			} {
				setup.Packing = p.packing
				setup.KeyKind = p.curve
				t.Run(fmt.Sprintf("packing=%v/curve=%v", p.packing, p.curve), func(t *testing.T) {
					testAllOnePacking(t, setup)
				})
			}
		})
	}
}

// checkDirEntry verifies a dir entry against expectations. size == 0 for don't check.
func checkDirEntry(t *testing.T, dirEntry *upspin.DirEntry, name upspin.PathName, hasLocation bool, size int) {
	if dirEntry.Name != name {
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
	dir, err := bind.DirServer(env.Context, env.Context.DirEndpoint())
	if err != nil {
		return err
	}

	fileSet1, err := dir.Glob(ownerName + "/*/*")
	if err != nil {
		return err
	}
	fileSet2, err := dir.Glob(ownerName + "/*")
	if err != nil {
		return err
	}
	entries := append(fileSet1, fileSet2...)
	var firstErr error
	deleteNow := func(name upspin.PathName) {
		err = dir.Delete(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("cleanup: error deleting %q: %v", name, err)
		}
	}
	// First, delete all Access files,
	// so we don't lock ourselves out if our tests above remove delete rights.
	for _, entry := range entries {
		if access.IsAccessFile(entry.Name) {
			deleteNow(entry.Name)
		}
	}
	for _, entry := range entries {
		if !access.IsAccessFile(entry.Name) {
			deleteNow(entry.Name)
		}
	}
	return firstErr
}
