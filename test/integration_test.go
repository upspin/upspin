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

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/path"
	e "upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
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
	setup = e.Setup{
		Tree: e.Tree{
			e.E("/dir1/", ""),
			e.E("/dir2/", ""),
			e.E("/dir1/file1.txt", contentsOfFile1),
			e.E("/dir2/file2.txt", contentsOfFile2),
			e.E("/dir2/file3.pdf", contentsOfFile3),
		},
		OwnerName:                 ownersName,
		Transport:                 upspin.GCP,
		IgnoreExistingDirectories: false, // left-over Access files would be a problem.
		Cleanup:                   deleteGCPEnv,
	}
	readerClient upspin.Client
)

func testNoReadersAllowed(t *testing.T, env *e.Env) {
	var err error
	fileName := upspin.PathName(ownersName + "/dir1/file1.txt")
	_, err = readerClient.Get(fileName)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error contains %s, got %s", access.ErrPermissionDenied, err)
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

func testAllowListAccess(t *testing.T, env *e.Env) {
	_, err := env.Client.Put(ownersName+"/dir1/Access", []byte("l:"+readersName))
	if err != nil {
		t.Fatal(err)
	}
	// Now check that readerClient can list file1, but can't read and therefore the Location is zeroed out.
	file := ownersName + "/dir1/file1.txt"
	dirs, err := readerClient.Glob(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), !hasLocation, len(contentsOfFile1))

	// Ensure we can't read the data.
	_, err = readerClient.Get(upspin.PathName(file))
	if err == nil {
		t.Errorf("Expected error, got none")
	}
	// TODO: this is not an ideal error message. We have list permission, but not read. Need to fix this.
	expectedError := "empty reference"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error contains %s, got %s", expectedError, err)
	}
}

func testAllowReadAccess(t *testing.T, env *e.Env) {
	// Owner has no delete permission.
	_, err := env.Client.Put(ownersName+"/dir1/Access", []byte("r:"+readersName+"\nc,w,l,r:"+ownersName))
	if err != nil {
		t.Fatal(err)
	}
	// Must Put the file again.
	// TODO: remove this from here when Update is ready.
	_, err = env.Client.Put(ownersName+"/dir1/file1.txt", []byte(contentsOfFile1))
	if err != nil {
		t.Fatal(err)
	}
	data, err := readerClient.Get(upspin.PathName(ownersName + "/dir1/file1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contentsOfFile1 {
		t.Errorf("Expected contents %q, got %q", contentsOfFile1, data)
	}
}

func testCreateAndOpen(t *testing.T, env *e.Env) {
	filePath := upspin.PathName(path.Join(ownersName, "myotherfile.txt"))
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

func testGlobWithLimitedAccess(t *testing.T, env *e.Env) {
	// Owner sees both files.
	pattern := ownersName + "/dir*/*.txt"
	dirs, err := env.Client.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("Expected 2 dirs, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
	checkDirEntry(t, dirs[1], upspin.PathName(ownersName+"/dir2/file2.txt"), hasLocation, len(contentsOfFile2))

	// readerClient should only be able to see contents of dir1 not dir2.
	dirs, err = readerClient.Glob(pattern)
	if len(dirs) != 1 {
		t.Fatalf("Expected 2 dirs, got %d", len(dirs))
	}
	checkDirEntry(t, dirs[0], upspin.PathName(ownersName+"/dir1/file1.txt"), hasLocation, len(contentsOfFile1))
}

func testGlobWithPattern(t *testing.T, env *e.Env) {
	c := env.Client

	for i := 0; i <= 10; i++ {
		dirPath := upspin.PathName(fmt.Sprintf("%s/mydir%d", ownersName, i))
		_, err := c.MakeDirectory(dirPath)
		log.Printf("mkdir %s: %s", dirPath, err)
		if err != nil && !strings.Contains(err.Error(), dirAlreadyExists) {
			t.Fatal(err)
		}
	}
	dirEntries, err := c.Glob(ownersName + "/mydir[0-1]*")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirEntries) != 3 {
		t.Fatalf("Expected 3 paths, got %d", len(dirEntries))
	}
	if string(dirEntries[0].Name) != ownersName+"/mydir0" {
		t.Errorf("Expected mydir0, got %s", dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != ownersName+"/mydir1" {
		t.Errorf("Expected mydir1, got %s", dirEntries[1].Name)
	}
	if string(dirEntries[2].Name) != ownersName+"/mydir10" {
		t.Errorf("Expected mydir10, got %s", dirEntries[2].Name)
	}
}

func testDelete(t *testing.T, env *e.Env) {
	pathName := upspin.PathName(ownersName + "/dir2/file3.pdf")
	log.Printf("Context: Username: %s", env.Context.UserName)
	err := env.Context.Directory.Delete(pathName)
	if err != nil {
		t.Fatal(err)
	}
	// Check it really deleted it (and is not being cached in memory).
	_, err = env.Client.Get(pathName)
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected error contains not found, got %s", err)
	}
	// But I can't delete files in dir1, since I lack permission.
	pathName = upspin.PathName(ownersName + "/dir1/file1.txt")
	err = env.Context.Directory.Delete(pathName)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	if !strings.Contains(err.Error(), access.ErrPermissionDenied.Error()) {
		t.Errorf("Expected error %s, got %s", access.ErrPermissionDenied, err)
	}
	// But we can always remove the Access file.
	accessPathName := upspin.PathName(ownersName + "/dir1/Access")
	err = env.Context.Directory.Delete(accessPathName)
	if err != nil {
		t.Fatal(err)
	}
	// Now delete file1.txt
	err = env.Context.Directory.Delete(pathName)
	if err != nil {
		t.Fatal(err)
	}
}

func testSharing(t *testing.T, env *e.Env) {
	const (
		sharedContent = "Hey man, whatup?"
	)
	var (
		sharedDir      = path.Join(ownersName, "mydir")
		sharedFilePath = path.Join(sharedDir, "sharedfile")
	)
	c := env.Client

	// Put an Access file where no one has access (this forces updating the parent dir with no access).
	accessFile := "r,c,w,d,l:" + ownersName // all rights to the owner.
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
	accessFile = fmt.Sprintf("r: %s\nr,c,w,d,l:%s", readersName, ownersName)
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

func testAllOnePacking(t *testing.T, packing upspin.Packing) {
	setup.Packing = packing
	var readersKey upspin.KeyPair
	switch packing {
	case upspin.EEp256Pack, upspin.EEp521Pack:
		setup.Keys = keyStore[setup.OwnerName][packing]
		readersKey = keyStore[readersName][packing]
	default:
		// Keys are needed with any packing type (even Plain) for auth purposes.
		setup.Keys = keyStore[setup.OwnerName][upspin.EEp256Pack]
		readersKey = keyStore[readersName][upspin.EEp256Pack]
	}

	env, err := e.New(&setup)
	if err != nil {
		t.Fatal(err)
	}

	readerClient, _, err = env.NewUser(readersName, &readersKey)
	if err != nil {
		t.Fatal(err)
	}

	// The ordering here is important as each test adds state to the tree.
	testNoReadersAllowed(t, env)
	testAllowListAccess(t, env)
	testAllowReadAccess(t, env)
	testCreateAndOpen(t, env)
	testGlobWithLimitedAccess(t, env)
	testGlobWithPattern(t, env)
	testDelete(t, env)

	err = env.Exit()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAll(t *testing.T) {
	for _, packing := range []upspin.Packing{
		upspin.PlainPack,
		upspin.EEp256Pack,
		upspin.DebugPack,
		upspin.EEp521Pack,
	} {
		log.Printf("=== Packing %d", packing)
		testAllOnePacking(t, packing)
	}
}

// checkDirEntry verifies a dir entry against expectations. size == 0 for don't check.
func checkDirEntry(t *testing.T, dirEntry *upspin.DirEntry, name upspin.PathName, hasLocation bool, size int) {
	if dirEntry.Name != name {
		t.Errorf("Expected name %s, got %s", name, dirEntry.Name)
	}
	var zeroLoc upspin.Location
	if dirEntry.Location == zeroLoc {
		if hasLocation {
			t.Errorf("Expected %s to have location", name)
		}
	} else {
		if !hasLocation {
			t.Errorf("Expected %s not to have location, got %v", name, dirEntry.Location)
		}
	}
	if size != 0 && dirEntry.Metadata.Size != uint64(size) {
		t.Errorf("Expected %s has size %d, got %d", name, size, dirEntry.Metadata.Size)
	}
}

func deleteGCPEnv(env *e.Env) error {
	fileSet1, err := env.Client.Glob(ownersName + "/*/*")
	if err != nil {
		return err
	}
	fileSet2, err := env.Client.Glob(ownersName + "/*")
	if err != nil {
		return err
	}
	entries := append(fileSet1, fileSet2...)
	var firstErr error
	deleteNow := func(name upspin.PathName) {
		err = env.Context.Directory.Delete(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("Error deleting %s: %s", name, err)
		}
	}
	// First, delete all Access files, so we don't lock ourselves out if our tests above remove delete rights.
	for _, entry := range entries {
		if strings.HasSuffix(string(entry.Name), "Access") {
			deleteNow(entry.Name)
		}
	}
	for _, entry := range entries {
		log.Printf("Deleting %s", entry.Name)
		deleteNow(entry.Name)
	}
	return firstErr
}
