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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/errors"
	"upspin.io/key/usercache"
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

const (
	ownerName  = "upspin-test@google.com"
	readerName = "upspin-friend-test@google.com"
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
	readerClient  upspin.Client
	readerContext upspin.Context
)

type testRunner struct {
	Entry   *upspin.DirEntry
	Entries []*upspin.DirEntry
	Data    string

	user    upspin.UserName
	clients map[upspin.UserName]upspin.Client

	err     error
	errFile string
	errLine int
	lastErr error // used by Diag
}

func newRunner() *testRunner {
	return &testRunner{clients: make(map[upspin.UserName]upspin.Client)}
}

func (r *testRunner) setErr(err error) {
	if r.err != nil {
		return
	}
	r.err = err
	_, r.errFile, r.errLine, _ = runtime.Caller(2)
}

func (r *testRunner) AddUser(ctx upspin.Context) *testRunner {
	if r.err != nil {
		return r
	}
	r.clients[ctx.UserName()] = client.New(ctx)
	return r
}

func (r *testRunner) As(u upspin.UserName) *testRunner {
	if r.err != nil {
		return r
	}
	c := r.clients[u]
	if c == nil {
		r.setErr(errors.E(errors.NotExist, u))
		return r
	}
	r.user = u
	return r
}

func (r *testRunner) Get(p upspin.PathName) *testRunner {
	if r.err != nil {
		return r
	}
	data, err := r.clients[r.user].Get(p)
	r.Data = string(data)
	r.setErr(err)
	return r
}

func (r *testRunner) Put(p upspin.PathName, data string) *testRunner {
	if r.err != nil {
		return r
	}
	entry, err := r.clients[r.user].Put(p, []byte(data))
	r.Entry = entry
	r.setErr(err)
	return r
}

func (r *testRunner) Glob(pattern string) *testRunner {
	if r.err != nil {
		return r
	}
	entries, err := r.clients[r.user].Glob(pattern)
	r.Entries = entries
	r.setErr(err)
	return r
}

func (r *testRunner) Err() error {
	err := r.err
	r.err = nil
	r.lastErr = err
	return err
}

func (r *testRunner) Diag() string {
	if r.lastErr == nil {
		return "<nil>"
	}
	if r.errFile == "" {
		return r.lastErr.Error()
	}
	return fmt.Sprintf("%v:%v: %v", filepath.Base(r.errFile), r.errLine, r.lastErr)
}

func testNoReadersAllowed(t *testing.T, env *testenv.Env) {
	r := newRunner().AddUser(env.Context).AddUser(readerContext)

	fileName := upspin.PathName(ownerName + "/dir1/file1.txt")

	r.As(readerName).Get(fileName)
	err := r.Err()
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error %q, got %v", access.ErrPermissionDenied, r.Diag())
	}
	// But the owner can still read it.
	r.As(ownerName).Get(fileName)
	if err := r.Err(); err != nil {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, r.Data)
	}
}

func testAllowListAccess(t *testing.T, env *testenv.Env) {
	r := newRunner().AddUser(env.Context).AddUser(readerContext)

	r.As(ownerName).Put(ownerName+"/dir1/Access", "l:"+readerName)

	// Check that readerClient can list file1, but can't read and therefore the Location is zeroed out.
	file := ownerName + "/dir1/file1.txt"
	r.As(readerName).Glob(file)
	if err := r.Err(); err != nil {
		t.Fatal(r.Diag())
	}
	if len(r.Entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(r.Entries))
	}
	checkDirEntry(t, r.Entries[0], upspin.PathName(ownerName+"/dir1/file1.txt"), !hasLocation, 0)

	// Ensure we can't read the data.
	r.As(readerName).Get(upspin.PathName(file))
	if err := r.Err(); err == nil {
		t.Errorf("Get(%q) succeeded, expected an error", file)
	}
	// TODO: the error differs between GCP and InProcess. The reason is
	// GCP Dir returns the DirEntry with Location and Packdata zeroed out,
	// while InProcess returns permission denied.
}

func testAllowReadAccess(t *testing.T, env *testenv.Env) {
	r := newRunner().AddUser(env.Context).AddUser(readerContext)

	// Owner has no delete permission (assumption tested in testDelete).
	r.As(ownerName)
	r.Put(ownerName+"/dir1/Access", "l,r:"+readerName+"\nc,w,l,r:"+ownerName)
	// Put file back again so we force keys to be re-wrapped.
	r.Put(ownerName+"/dir1/file1.txt", contentsOfFile1)

	// Now try reading as the reader.
	r.As(readerName).Get(upspin.PathName(ownerName + "/dir1/file1.txt"))
	if err := r.Err(); err != nil {
		t.Fatal(r.Diag())
	}
	if r.Data != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, r.Data)
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

	/* TODO: GCP has a bug in complex Glob expressions and Access file matching.
	                 Test disabled for now until new DirServer is ready.
		// Without list access to the root, the reader can't glob /dir*.
		dirs, err = readerClient.Glob(bothPat)
		if err != nil {
			t.Fatal(err)
		}
		checkDirs("reader", bothPat, dirs, 0)
	*/

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
	_, err = dir.Delete(pathName)
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
	_, err = dir.Delete(pathName)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error %s, got %s", access.ErrPermissionDenied, err)
	}
	// But we can always remove the Access file.
	accessPathName := upspin.PathName(ownerName + "/dir1/Access")
	_, err = dir.Delete(accessPathName)
	if err != nil {
		t.Fatal(err)
	}
	// Now delete file1.txt
	_, err = dir.Delete(pathName)
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
	usercache.ResetGlobal()

	env, err := testenv.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	readerClient, readerContext, err = env.NewUser(readerName)
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
	for _, kind := range []string{"inprocess"} {
		//for _, kind := range []string{"inprocess", "gcp"} {
		t.Run(fmt.Sprintf("kind=%v", kind), func(t *testing.T) {
			setup := setupTemplate
			setup.Kind = kind
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
		_, err = dir.Delete(name)
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

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("test/testenv: no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
