package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/auth/testauth"
	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	userName       = "test@foo.com"
	parentPathName = userName + "/mydir"
	pathName       = parentPathName + "/myfile.txt"
)

var (
	dummySess = testauth.NewSessionForTesting(userName, false, nil)
	dir       = upspin.DirEntry{
		Name: upspin.PathName(pathName),
		Metadata: upspin.Metadata{
			IsDir: false,
			Size:  32,
			Time:  upspin.Now(),
		},
	}
	dirParent = upspin.DirEntry{
		Name: upspin.PathName(parentPathName),
		Metadata: upspin.Metadata{
			IsDir: true,
		},
	}
)

func Put(t *testing.T, ds *dirServer, dirEntry upspin.DirEntry, errorExpected string) {
	resp := nettest.NewExpectingResponseWriter(errorExpected)
	jsonStr := toJSON(t, dirEntry)
	req, err := http.NewRequest("POST", "http://localhost:8080/put", bytes.NewBuffer(jsonStr))
	if err != nil {
		t.Fatalf("Can't make new request: %v", err)
	}
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorParseRoot(t *testing.T) {
	// No path given
	Put(t, newDummyDirServer(), upspin.DirEntry{}, `{"error":"DirService: no user name in path"}`)
}

func TestPutErrorParseUser(t *testing.T) {
	dir := upspin.DirEntry{
		Name: upspin.PathName("a@x/myroot/myfile"),
	}
	Put(t, newDummyDirServer(), dir, `{"error":"DirService: no user name in path"}`)
}

func makeValidMeta() upspin.Metadata {
	return upspin.Metadata{
		IsDir:    true,
		Sequence: 0,
	}
}

func TestPutErrorInvalidSequenceNumber(t *testing.T) {
	meta := makeValidMeta()
	meta.Sequence = -1
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: meta,
	}
	Put(t, newDummyDirServer(), dir, `{"error":"DirService: verifyMeta: fred@bob.com/myroot/myfile: invalid sequence number"}`)
}

func TestLookupPathError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Get: missing pathname in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get", nil)

	ds := newDummyDirServer()
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGlobMissingPattern(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Glob: missing pattern in request"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/glob", nil)

	ds := newDummyDirServer()
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGlobBadPath(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Glob: missing/email/dir/file: bad user name in path"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/list?pattern=missing/email/dir/file", nil)

	ds := newDummyDirServer()
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorFileNoParentDir(t *testing.T) {
	dir := upspin.DirEntry{
		Name:     upspin.PathName("fred@bob.com/myroot/myfile"),
		Metadata: makeValidMeta(),
	}
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"something that does not match"},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	Put(t, ds, dir, `{"error":"DirService: Put: fred@bob.com/myroot/myfile: parent path not found"}`)
}

func TestLookupPathNotFound(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: get: o@foo.bar/invalid/invalid/invalid: path not found"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/get?pathname=o@foo.bar/invalid/invalid/invalid", nil)
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"something that does not match"},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGlobComplex(t *testing.T) {
	// Create dir entries for files that match that will be looked up after Globbing.
	dir1 := upspin.DirEntry{
		Name: "f@b.co/subdir/a.pdf",
	}
	dir1JSON := toJSON(t, dir1)
	dir2 := upspin.DirEntry{
		Name: "f@b.co/subdir2/b.pdf",
	}
	dir2JSON := toJSON(t, dir2)
	// dir3 is a match, but is not readable to user.
	// TODO: make dir3 not readable to user.
	dir3 := upspin.DirEntry{
		Name: "f@b.co/subdir3/c.pdf",
	}
	dir3JSON := toJSON(t, dir3)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{"f@b.co/subdir/a.pdf", "f@b.co/subdir2/b.pdf", "f@b.co/subdir3/c.pdf"},
			Data: [][]byte{dir1JSON, dir2JSON, dir3JSON},
		},
		prefix: "f@b.co/",
		fileNames: []string{"f@b.co/subdir/a.pdf", "f@b.co/otherdir/b.pdf", "f@b.co/subfile",
			"f@b.co/subdir/notpdf", "f@b.co/subdir2/b.pdf", "f@b.co/subdir3/c.pdf"},
	}

	// TODO: remove dir3 from response.
	respBody := toJSON(t, []upspin.DirEntry{dir1, dir2, dir3}) // dir3 is NOT returned to user (no access)
	resp := nettest.NewExpectingResponseWriter(string(respBody))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8081/glob?pattern=f@b.co/sub*/*.pdf", nil)

	ds := newDirServer(lgcp, newDummyStoreClient())
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)

	if lgcp.listDirCalled {
		t.Error("Call to ListDir unexpected")
	}
	if !lgcp.listPrefixCalled {
		t.Error("Expected call to ListPrefix")
	}
}

func TestGlobSimple(t *testing.T) {
	// Create dir entries for files that match that will be looked up after Globbing.
	dir1 := upspin.DirEntry{
		Name: "f@b.co/subdir/a.pdf",
	}
	dir1JSON := toJSON(t, dir1)
	dir2 := upspin.DirEntry{
		Name: "f@b.co/subdir/b.pdf",
	}
	dir2JSON := toJSON(t, dir2)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{"f@b.co/subdir/a.pdf", "f@b.co/subdir/b.pdf"},
			Data: [][]byte{dir1JSON, dir2JSON},
		},
		prefix: "f@b.co/subdir/",
		fileNames: []string{"f@b.co/subdir/a.pdf", "f@b.co/subdir/bpdf", "f@b.co/subdir/foo",
			"f@b.co/subdir/notpdf", "f@b.co/subdir/b.pdf"},
	}

	respBody := toJSON(t, []upspin.DirEntry{dir1, dir2})
	resp := nettest.NewExpectingResponseWriter(string(respBody))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8081/glob?pattern=f@b.co/subdir/*.pdf", nil)

	ds := newDirServer(lgcp, newDummyStoreClient())
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)

	if !lgcp.listDirCalled {
		t.Error("Expected call to ListDir")
	}
	if lgcp.listPrefixCalled {
		t.Error("Unexpected call to ListPrefix")
	}
}

func TestPutParentNotDir(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)
	// The DirEntry of the parent, converted to JSON.
	notDirParent := dirParent
	notDirParent.Metadata.IsDir = false // Parent is not dir!
	dirParentJSON := toJSON(t, notDirParent)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: parent is not a directory"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{parentPathName},
		Data: [][]byte{dirParentJSON},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutFileOverwritesDir(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)
	// The DirEntry of the parent, converted to JSON.
	dirParentJSON := toJSON(t, dirParent)

	// The dir entry we're trying to add already exists as a directory.
	existingDirEntry := dir
	existingDirEntry.Metadata.IsDir = true
	existingDirEntryJSON := toJSON(t, existingDirEntry)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: directory already exists"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{parentPathName, pathName},
		Data: [][]byte{dirParentJSON, existingDirEntryJSON},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutDirOverwritesFile(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	newDir := dir
	newDir.Metadata.IsDir = true
	dirEntryJSON := toJSON(t, newDir)

	// The DirEntry of the parent, converted to JSON.
	dirParentJSON := toJSON(t, dirParent)

	// The dir entry we're trying to add already exists as a file.
	existingDirEntry := dir
	existingDirEntryJSON := toJSON(t, existingDirEntry)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: overwriting file with directory"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{parentPathName, pathName},
		Data: [][]byte{dirParentJSON, existingDirEntryJSON},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPut(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)

	// The DirEntry of the parent, converted to JSON.
	dirParentJSON := toJSON(t, dirParent)

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{"test@foo.com/mydir"},
		Data: [][]byte{dirParentJSON},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	// Check that the parent Sequence number was updated...
	updatedParent := dirParent
	updatedParent.Metadata.Sequence++
	updatedParentJSON := toJSON(t, updatedParent)

	// And that the file's Readers were updated
	updatedDir := dir
	updatedDirJSON := toJSON(t, updatedDir)

	// Verify what was actually put
	if len(egcp.PutContents) != 2 {
		t.Fatalf("Expected put to write 2 dir entries, got %d", len(egcp.PutContents))
	}
	if egcp.PutRef[0] != string(dir.Name) {
		t.Errorf("Expected put to write to %s, wrote to %s", dir.Name, egcp.PutRef)
	}
	if !bytes.Equal(updatedDirJSON, egcp.PutContents[0]) {
		t.Errorf("Expected put to write %s, wrote %s", updatedDirJSON, egcp.PutContents[0])
	}
	if egcp.PutRef[1] != string(dirParent.Name) {
		t.Errorf("Expected put to write to %s, wrote to %s", dirParent.Name, egcp.PutRef[1])
	}
	if !bytes.Equal(updatedParentJSON, egcp.PutContents[1]) {
		t.Errorf("Expected put to write %s, wrote %s", updatedParentJSON, egcp.PutContents[1])
	}
}

func TestPutRoot(t *testing.T) {
	const user = "dude@foo.com"
	rootDir := &upspin.DirEntry{
		Name: user + "/",
		Metadata: upspin.Metadata{
			IsDir:    true,
			Sequence: 0,
			Time:     72,
		},
	}
	root := &root{
		dirEntry: rootDir,
	}

	// rootDirJSON is what the client requests...
	rootDirJSON := toJSON(t, rootDir)
	// ... rootJSON is what the server puts to GCP.
	rootJSON := toRootJSON(t, root)

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", rootDirJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"does not exist"},
	}

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	if len(egcp.PutContents) != 1 {
		t.Fatalf("Expected put to write 1 dir entry, got %d", len(egcp.PutContents))
	}
	if egcp.PutRef[0] != user {
		t.Errorf("Expected put to write to %s, wrote to %s", user, egcp.PutRef)
	}
	if !bytes.Equal(rootJSON, egcp.PutContents[0]) {
		t.Errorf("Expected put to write %s, wrote %s", rootJSON, egcp.PutContents[0])
	}
}

func TestMarshalRoot(t *testing.T) {
	var (
		fileInRoot       = upspin.PathName("me@here.com/foo.txt")
		dirRestricted    = upspin.PathName("me@here.com/restricted")
		accessRoot       = upspin.PathName("me@here.com/Access")
		accessRestricted = upspin.PathName("me@here.com/restricted/Access")
	)
	acc1 := makeAccess(t, accessRoot, "r: bob@foo.com\nw: marie@curie.fr")
	acc2 := makeAccess(t, accessRestricted, "l: gandhi@peace.in")
	root := &root{
		dirEntry: &upspin.DirEntry{
			Name: upspin.PathName("me@here.com/"),
			Metadata: upspin.Metadata{
				IsDir: true,
			},
		},
		accessFiles: accessFileDB{accessRoot: acc1, accessRestricted: acc2},
	}
	buf := toRootJSON(t, root)
	root2, err := unmarshalRoot(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(root2.accessFiles) != 2 {
		t.Fatalf("Expected two Access files, got %d", len(root2.accessFiles))
	}
	// Make a few assertions about who can access what.
	// What I really want here is acc1.Equal(acc1saved), but Equal is not publicly implemented. :-(
	acc1saved, ok := root2.accessFiles[accessRoot]
	if !ok {
		t.Fatalf("Expected %s to exist in DB.", accessRoot)
	}
	can, _, err := acc1saved.Can(upspin.UserName("bob@foo.com"), access.Read, fileInRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Errorf("Expected bob@foo.com to have Read access to %s", fileInRoot)
	}
	acc2saved, ok := root2.accessFiles[accessRestricted]
	can, _, err = acc2saved.Can(upspin.UserName("gandhi@peace.in"), access.List, dirRestricted)
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Errorf("Expected gandhi@peace.in to have List access to %s", dirRestricted)
	}
}

func TestClientSendsBadDirEntry(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: unmarshal: invalid character 'c' looking for beginning of value"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/put", []byte("crap data"))

	ds := newDirServer(&gcptest.DummyGCP{}, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGCPCorruptsData(t *testing.T) {
	dirEntryJSON := toJSON(t, dir)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{pathName},
		Data: [][]byte{[]byte("really bad JSON structure that does not parse")},
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: getmeta: test@foo.com/mydir/myfile.txt: json unmarshal failed retrieving metadata: invalid character 'r' looking for beginning of value"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/get?pathname="+pathName, dirEntryJSON)

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGet(t *testing.T) {
	dirEntryJSON := toJSON(t, dir)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{pathName},
		Data: [][]byte{dirEntryJSON},
	}

	resp := nettest.NewExpectingResponseWriter(string(dirEntryJSON))
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/get?pathname="+pathName, dirEntryJSON)

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.getHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPatchErrorUpdateLocation(t *testing.T) {
	updateDir := upspin.DirEntry{
		Name: pathName,
		Location: upspin.Location{
			Reference: "new ref",
		},
	}
	updateDirJSON := toJSON(t, updateDir)
	dirEntryJSON := toJSON(t, dir) // original directory entry
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{pathName},
		Data: [][]byte{dirEntryJSON}, // original directory entry
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: patch: test@foo.com/mydir/myfile.txt: Location is not updatable"}`)
	req := nettest.NewRequest(t, netutil.Patch, "http://localhost:8081/put", updateDirJSON)

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req) // putHandler handles /put PATCH requests too
	resp.Verify(t)
}

func TestPatchErrorPathNotFound(t *testing.T) {
	updateDir := upspin.DirEntry{
		Name: pathName,
	}
	updateDirJSON := toJSON(t, updateDir)
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"does not match"},
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: download: pathname not found"}`)
	req := nettest.NewRequest(t, netutil.Patch, "http://localhost:8081/put", updateDirJSON)

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req) // putHandler handles /put PATCH requests too
	resp.Verify(t)
}

func TestPatch(t *testing.T) {
	updateDir := upspin.DirEntry{
		Name: pathName,
		Metadata: upspin.Metadata{
			Sequence: 39,
			Time:     upspin.Time(2),
			Size:     42,
			PackData: []byte("new packdata too"),
		},
	}
	updateDirJSON := toJSON(t, updateDir)
	dirEntryJSON := toJSON(t, dir) // original directory entry
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{pathName},
		Data: [][]byte{dirEntryJSON}, // original directory entry
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Patch, "http://localhost:8081/put", updateDirJSON)

	ds := newDirServer(egcp, newDummyStoreClient())
	ds.dirHandler(dummySess, resp, req) // dirHandler handles /put PATCH requests too
	resp.Verify(t)

	// Now verify that the DirEntry that was put is the one with the update.
	if len(egcp.PutContents) != 1 {
		t.Fatalf("Expected patch to write one dir entry, got %d", len(egcp.PutContents))
	}
	var writtenDirEntry upspin.DirEntry
	err := json.Unmarshal(egcp.PutContents[0], &writtenDirEntry)
	if err != nil {
		t.Fatal(err)
	}
	if writtenDirEntry.Name != pathName {
		t.Errorf("Expected path %s, got %s", pathName, writtenDirEntry.Name)
	}
	if writtenDirEntry.Metadata.Sequence != updateDir.Metadata.Sequence {
		t.Errorf("Expected sequence %d, got %d", updateDir.Metadata.Sequence, writtenDirEntry.Metadata.Sequence)
	}
	if writtenDirEntry.Metadata.Time != updateDir.Metadata.Time {
		t.Errorf("Expected time %d, got %d", updateDir.Metadata.Time, writtenDirEntry.Metadata.Time)
	}
	if writtenDirEntry.Metadata.Size != updateDir.Metadata.Size {
		t.Errorf("Expected time %d, got %d", updateDir.Metadata.Size, writtenDirEntry.Metadata.Size)
	}
	if string(writtenDirEntry.Metadata.PackData) != string(updateDir.Metadata.PackData) {
		t.Errorf("Expected packdata %s, got %s", updateDir.Metadata.PackData, writtenDirEntry.Metadata.PackData)
	}
}

func toJSON(t *testing.T, data interface{}) []byte {
	ret, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return ret
}

func toRootJSON(t *testing.T, root *root) []byte {
	json, err := marshalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return json
}

func makeAccess(t *testing.T, path upspin.PathName, accessFileContents string) *access.Access {
	acc, err := access.Parse(path, []byte(accessFileContents))
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func newDummyDirServer() *dirServer {
	return newDirServer(&gcptest.DummyGCP{}, newDummyStoreClient())
}

func newDummyStoreClient() *storeClient {
	mock := nettest.NewMockHTTPClient(nil, nil)
	authCli := auth.NewClient(upspin.UserName("this-server@upspin.io"), upspin.KeyPair{}, mock)
	return newStoreClient(authCli)
}

// listGCP is an ExpectDownloadCapturePutGCP that returns a slice of fileNames
// if a call to ListPrefix or ListDir matches the expected prefix or dir.
type listGCP struct {
	gcptest.ExpectDownloadCapturePutGCP
	prefix           string
	fileNames        []string
	listPrefixCalled bool
	listDirCalled    bool
}

func (l *listGCP) ListPrefix(prefix string, depth int) ([]string, error) {
	l.listPrefixCalled = true
	if l.prefix == prefix {
		return l.fileNames, nil
	}
	return []string{}, errors.New("Not found")
}

func (l *listGCP) ListDir(dir string) ([]string, error) {
	l.listDirCalled = true
	if l.prefix == dir {
		return l.fileNames, nil
	}
	return []string{}, errors.New("Not found")
}
