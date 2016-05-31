package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"upspin.io/access"
	"upspin.io/auth/testauth"
	"upspin.io/cloud/gcp"
	"upspin.io/cloud/gcp/gcptest"
	"upspin.io/cloud/netutil"
	"upspin.io/cloud/netutil/nettest"
	"upspin.io/factotum"
	"upspin.io/upspin"
)

const (
	userName       = "test@foo.com"
	rootPath       = userName + "/"
	parentPathName = userName + "/mydir"
	pathName       = parentPathName + "/myfile.txt"
	rootAccessFile = userName + "/Access"
)

var (
	dummySess = testauth.NewSessionForTesting(userName, false, nil)
	dir       = upspin.DirEntry{
		Name: upspin.PathName(pathName),
		Location: upspin.Location{
			Reference: upspin.Reference("1234"),
			Endpoint: upspin.Endpoint{
				Transport: upspin.GCP,
				NetAddr:   "https://store-server.com",
			},
		},
		Metadata: upspin.Metadata{
			Attr:     upspin.AttrNone,
			Size:     32,
			Time:     upspin.Now(),
			Packdata: []byte("12345"),
		},
	}
	dirParent = upspin.DirEntry{
		Name: upspin.PathName(parentPathName),
		Metadata: upspin.Metadata{
			Attr: upspin.AttrDirectory,
		},
	}
	defaultAccess, _ = access.New(rootAccessFile)
	userRoot         = root{
		dirEntry: upspin.DirEntry{
			Name: rootPath,
			Location: upspin.Location{
				// Reference is empty for the root.
				Endpoint: upspin.Endpoint{
					Transport: upspin.GCP,
					NetAddr:   "https://directory-server.com",
				},
			},
			Metadata: upspin.Metadata{
				Attr: upspin.AttrDirectory,
			},
		},
		accessFiles: accessFileDB{rootAccessFile: defaultAccess},
	}
	dirVal = fmt.Sprintf("%d", upspin.AttrDirectory)
	// These are not real keys. Just *valid* keys so authClient does not complain.
	serverKeys = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
)

func Put(t *testing.T, ds *dirServer, dirEntry upspin.DirEntry, errorExpected string) {
	resp := nettest.NewExpectingResponseWriter(errorExpected)
	jsonDir := toJSON(t, dirEntry)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", dirEntry.Name), jsonDir)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorParseRoot(t *testing.T) {
	// No path given
	Put(t, newTestDirServer(t, &gcptest.DummyGCP{}), upspin.DirEntry{}, `{"error":"DirService: POST: invalid pathname"}`)
}

func TestPutErrorParseUser(t *testing.T) {
	dir := upspin.DirEntry{
		Name: upspin.PathName("a@x/myroot/myfile"),
	}
	Put(t, newTestDirServer(t, &gcptest.DummyGCP{}), dir, `{"error":"DirService: POST: a@x/myroot/myfile: no user name in path"}`)
}

func makeValidMeta() upspin.Metadata {
	return upspin.Metadata{
		Attr:     upspin.AttrDirectory,
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
	Put(t, newTestDirServer(t, &gcptest.DummyGCP{}), dir, `{"error":"DirService: verifyMeta: fred@bob.com/myroot/myfile: invalid sequence number"}`)
}

func TestLookupPathError(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: GET: invalid pathname"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir", nil)

	ds := newTestDirServer(t, &gcptest.DummyGCP{})
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGlobMissingPattern(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Glob: invalid pathname"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/glob/", nil)

	ds := newTestDirServer(t, &gcptest.DummyGCP{})
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGlobBadPath(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Glob: missing/email/dir/file: bad user name in path"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/glob/missing/email/dir/file", nil)

	ds := newTestDirServer(t, &gcptest.DummyGCP{})
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutErrorFileNoParentDir(t *testing.T) {
	dir := upspin.DirEntry{
		Name:     upspin.PathName("test@foo.com/myroot/myfile"),
		Metadata: makeValidMeta(),
	}
	rootJSON := toRootJSON(t, &userRoot)
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, "something that does not match"},
		Data: [][]byte{rootJSON, []byte("")},
	}

	ds := newTestDirServer(t, egcp)
	Put(t, ds, dir, `{"error":"DirService: Put: test@foo.com/myroot/myfile: parent path not found"}`)
}

func TestLookupPathNotFound(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)
	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: get: test@foo.com/invalid/invalid/invalid: path not found"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/test@foo.com/invalid/invalid/invalid", nil)
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, "something that does not match"},
		Data: [][]byte{rootJSON, []byte("")},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

// Regression test to catch that we don't panic (by going past the root).
func TestLookupRoot(t *testing.T) {
	// The root converted to JSON.
	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"Name":"test@foo.com/","Location":{"Endpoint":{"Transport":1,"NetAddr":"https://directory-server.com"},"Reference":""},"Metadata":{"Attr":` + dirVal + `,"Sequence":0,"Size":0,"Time":0,"Packdata":null}}`)

	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+userName+"/", nil)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestLookup(t *testing.T) {
	// The root converted to JSON.
	rootJSON := toRootJSON(t, &userRoot)

	// Non-default Location
	resp := nettest.NewExpectingResponseWriter(`{"Name":"test@foo.com/","Location":{"Endpoint":{"Transport":1,"NetAddr":"https://directory-server.com"},"Reference":""},"Metadata":{"Attr":` + dirVal + `,"Sequence":0,"Size":0,"Time":0,"Packdata":null}}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+userName+"/", nil)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestLookupWithoutReadRights(t *testing.T) {
	// The root converted to JSON.
	newRoot := userRoot
	newRoot.accessFiles = make(accessFileDB)
	// lister-dude@me.com can only List, not read. Therefore the Lookup operation clears out Location.
	newRoot.accessFiles[rootAccessFile] = makeAccess(t, rootAccessFile, "l:lister-dude@me.com")
	rootJSON := toRootJSON(t, &newRoot)

	dirJSON := toJSON(t, dir)
	dirAnswer := dir // make a copy.
	dirAnswer.Metadata.Packdata = nil
	dirAnswer.Location = upspin.Location{}
	dirAnswerJSON := toJSON(t, dirAnswer)

	// Default, zero Location.
	resp := nettest.NewExpectingResponseWriter(string(dirAnswerJSON))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+pathName, nil)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, pathName},
		Data: [][]byte{rootJSON, dirJSON},
	}

	ds := newTestDirServer(t, egcp)
	session := testauth.NewSessionForTesting("lister-dude@me.com", false, nil)
	ds.dirHandler(session, resp, req)
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
	dir3 := upspin.DirEntry{
		Name: "f@b.co/subdir3/c.pdf",
	}
	dir3JSON := toJSON(t, dir3)

	// Seed the root with two pre-parsed Access files granting read permission to userName on subdir1 and subdir2.
	acc1 := makeAccess(t, "f@b.co/Access", "l: "+userName) // current user has list access
	acc2 := makeAccess(t, "f@b.co/subdir3/Access", "")     // No one has access
	root := &root{
		dirEntry: upspin.DirEntry{
			Name: "f@b.co/",
		},
		accessFiles: map[upspin.PathName]*access.Access{"f@b.co/Access": acc1, "f@b.co/subdir3/Access": acc2},
	}
	rootJSON := toRootJSON(t, root)

	// Order of events:
	// 1) List all files in the prefix.
	// 2) Lookup the first one. Discover its path.
	// 3) Fetch root to find all Access files to see if one matches the first returned file. The root Access file grants permission.
	// 4) Lookup the second one. Discover its path. Root is in cache. Apply check. It passes for same reason.
	// 5) Lookup the the third one. Discover its path. Root is in cache. Discover Access file that rules it. It fails.
	// 5) Return files to user.
	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{"f@b.co/subdir/a.pdf", "f@b.co", "f@b.co/subdir2/b.pdf", "f@b.co/subdir3/c.pdf"},
			Data: [][]byte{dir1JSON, rootJSON, dir2JSON, dir3JSON},
		},
		prefix: "f@b.co/",
		fileNames: []string{"f@b.co/subdir/a.pdf", "f@b.co/otherdir/b.pdf", "f@b.co/subfile",
			"f@b.co/subdir/notpdf", "f@b.co/subdir2/b.pdf", "f@b.co/subdir3/c.pdf"},
	}

	respBody := toJSON(t, []upspin.DirEntry{dir1, dir2}) // dir3 is NOT returned to user (no access)
	resp := nettest.NewExpectingResponseWriter(string(respBody))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8081/glob/f@b.co/sub*/*.pdf", nil)

	ds := newTestDirServer(t, lgcp)
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
	// All files belong to the owner (userName) and hence no special Access files are needed, just the default one
	// at the root.
	dir1 := upspin.DirEntry{
		Name: userName + "/subdir/a.pdf",
		Location: upspin.Location{
			Reference: upspin.Reference("xxxx"),
		},
		Metadata: upspin.Metadata{
			Packdata: []byte("blah"),
		},
	}
	dir1JSON := toJSON(t, dir1)
	dir2 := upspin.DirEntry{
		Name: userName + "/subdir/b.pdf",
		Location: upspin.Location{
			Reference: upspin.Reference("yyyy"),
		},
		Metadata: upspin.Metadata{
			Packdata: []byte("bleh"),
		},
	}
	dir2JSON := toJSON(t, dir2)

	newRoot := userRoot
	newRoot.accessFiles = make(accessFileDB)
	// Access file gives the owner full list and read rights, but listerdude can only list, but not see the Location.
	newRoot.accessFiles[rootAccessFile] = makeAccess(t, rootAccessFile, "r,l: test@foo.com\n l: listerdude@me.com")
	rootJSON := toRootJSON(t, &newRoot)

	// Order of events:
	// 1) List all files in the prefix.
	// 2) Lookup the first one. Discover its path.
	// 3) Fetch root to find all Access files to see if one matches the first returned file. It does (implicitly).
	// 4) Lookup the second one. Discover its path. Root is in cache. Apply check. It passes (implicitly again).
	// 5) Return files to user.
	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName + "/subdir/a.pdf", userName, userName + "/subdir/b.pdf"},
			Data: [][]byte{dir1JSON, rootJSON, dir2JSON},
		},
		prefix: userName + "/subdir/",
		fileNames: []string{userName + "/subdir/a.pdf", userName + "/subdir/bpdf", userName + "/subdir/foo",
			userName + "/subdir/notpdf", userName + "/subdir/b.pdf"},
	}

	respBody := toJSON(t, []upspin.DirEntry{dir1, dir2})
	resp := nettest.NewExpectingResponseWriter(string(respBody))
	req := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("http://localhost:8081/glob/%s/subdir/*.pdf", userName), nil)

	ds := newTestDirServer(t, lgcp)
	ds.globHandler(dummySess, resp, req)
	resp.Verify(t)

	if !lgcp.listDirCalled {
		t.Error("Expected call to ListDir")
	}
	if lgcp.listPrefixCalled {
		t.Error("Unexpected call to ListPrefix")
	}

	// Now check that another user who doesn't have read permission, but does have list permission would get the
	// same list, but without the location in them.
	session := testauth.NewSessionForTesting(upspin.UserName("listerdude@me.com"), false, nil)
	// Location and Packdata are anonymized.
	dir1.Location = upspin.Location{}
	dir2.Location = upspin.Location{}
	dir1.Metadata.Packdata = nil
	dir2.Metadata.Packdata = nil
	respBody = toJSON(t, []upspin.DirEntry{dir1, dir2}) // new expected response does not have Location.
	resp = nettest.NewExpectingResponseWriter(string(respBody))

	ds.globHandler(session, resp, req) // using session for listerdude@me.com.
	resp.Verify(t)
}

func TestPutParentNotDir(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)
	// The DirEntry of the parent, converted to JSON.
	notDirParent := dirParent
	notDirParent.Metadata.Attr = upspin.AttrNone // Parent is not dir!
	dirParentJSON := toJSON(t, notDirParent)

	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: parent is not a directory"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", dir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, parentPathName},
		Data: [][]byte{rootJSON, dirParentJSON},
	}

	ds := newTestDirServer(t, egcp)
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
	existingDirEntry.SetDir()
	existingDirEntryJSON := toJSON(t, existingDirEntry)

	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: directory already exists"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", dir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, parentPathName, pathName},
		Data: [][]byte{rootJSON, dirParentJSON, existingDirEntryJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutDirOverwritesFile(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	newDir := dir
	newDir.SetDir()
	dirEntryJSON := toJSON(t, newDir)

	// The DirEntry of the parent, converted to JSON.
	dirParentJSON := toJSON(t, dirParent)

	// The dir entry we're trying to add already exists as a file.
	existingDirEntry := dir
	existingDirEntryJSON := toJSON(t, existingDirEntry)

	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: overwriting file with directory"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", newDir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, parentPathName, pathName},
		Data: [][]byte{rootJSON, dirParentJSON, existingDirEntryJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPutPermissionDenied(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)

	newRoot := userRoot
	newRoot.accessFiles = make(accessFileDB)
	newRoot.accessFiles[rootAccessFile] = makeAccess(t, rootAccessFile, "") // No one can write, including owner.
	rootJSON := toRootJSON(t, &newRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/mydir/myfile.txt: permission denied"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", dir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestPut(t *testing.T) {
	// The DirEntry we're trying to Put, converted to JSON.
	dirEntryJSON := toJSON(t, dir)

	// The DirEntry of the parent, converted to JSON.
	dirParentJSON := toJSON(t, dirParent)

	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", dir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, "test@foo.com/mydir"},
		Data: [][]byte{rootJSON, dirParentJSON},
	}

	ds := newTestDirServer(t, egcp)
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
	// rootDirJSON is what the client requests...
	rootDirJSON := toJSON(t, userRoot.dirEntry)
	// ... rootJSON is what the server puts to GCP.
	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", userRoot.dirEntry.Name), rootDirJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"does not exist"},
	}

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	if len(egcp.PutContents) != 1 {
		t.Fatalf("Expected put to write 1 dir entry, got %d", len(egcp.PutContents))
	}
	if egcp.PutRef[0] != userName {
		t.Errorf("Expected put to write to %s, wrote to %s", userName, egcp.PutRef)
	}
	if !bytes.Equal(rootJSON, egcp.PutContents[0]) {
		t.Errorf("Expected put to write %s, wrote %s", rootJSON, egcp.PutContents[0])
	}
}

func TestPutRootPermissionDenied(t *testing.T) {
	// rootDirJSON is what the client requests.
	rootDirJSON := toJSON(t, userRoot.dirEntry)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Put: test@foo.com/: permission denied"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8080/dir/", userRoot.dirEntry.Name), rootDirJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref: []string{"does not exist"},
	}

	ds := newTestDirServer(t, egcp)

	// The session is for a user other than the expected root owner.
	session := testauth.NewSessionForTesting(upspin.UserName("bozo@theclown.org"), false, nil)
	ds.dirHandler(session, resp, req)
	resp.Verify(t)

	if len(egcp.PutContents) != 0 {
		t.Fatalf("Expected put to write 0 dir entries, got %d", len(egcp.PutContents))
	}
}

func TestPutAccessFile(t *testing.T) {
	var (
		parentDir      = userName + "/subdir"
		accessPath     = parentDir + "/Access"
		accessContents = "r: mom@me.com\nl: bro@me.com"
	)

	// The DirEntry we're trying to Put, converted to JSON.
	dir := upspin.DirEntry{
		Name: upspin.PathName(accessPath),
		Location: upspin.Location{
			Reference: "1234",
			Endpoint: upspin.Endpoint{
				Transport: upspin.GCP,
				NetAddr:   upspin.NetAddr("https://store-server.upspin.io"),
			},
		},
	}
	dirEntryJSON := toJSON(t, dir)

	// The DirEntry of the root.
	rootJSON := toRootJSON(t, &userRoot)

	// The DirEntry of the parent
	dirParent := upspin.DirEntry{
		Name: upspin.PathName(parentDir),
		Metadata: upspin.Metadata{
			Attr: upspin.AttrDirectory,
		},
	}
	dirParentJSON := toJSON(t, dirParent)

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, u("http://localhost:8081/dir/", dir.Name), dirEntryJSON)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, parentDir},
		Data: [][]byte{rootJSON, dirParentJSON},
	}

	// Setup the directory's store client to return the contents of the access file.
	f, err := factotum.New(serverKeys)
	if err != nil {
		t.Fatal(err)
	}

	ds := newDirServer(egcp, f,
		func(e upspin.Endpoint) (upspin.Store, error) {
			return &dummyStore{
				ref:      upspin.Reference("1234"),
				contents: []byte(accessContents),
			}, nil
		})
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	// And the server Put a new root to GCP, the Access file and incremented the parent's sequence.
	if len(egcp.PutRef) != 3 {
		t.Fatalf("Expected one Put, got %d", len(egcp.PutRef))
	}
	// First, store the Access file.
	if egcp.PutRef[0] != accessPath {
		t.Errorf("Expected put to %s, got %s", accessPath, egcp.PutRef[0])
	}
	// Then update the root.
	if egcp.PutRef[1] != parentDir {
		t.Errorf("Expected put to %s, got %s", parentDir, egcp.PutRef[1])
	}
	// Then update the parent.
	if egcp.PutRef[2] != userName {
		t.Errorf("Expected put to %s, got %s", userName, egcp.PutRef[2])
	}
	// Check that the root was updated with the new Access file.
	acc := makeAccess(t, upspin.PathName(accessPath), accessContents)
	expectedRoot := userRoot // Shallow copy
	expectedRoot.accessFiles = make(accessFileDB)
	// Copy map instead of modifying a test global (that will be re-used later)
	for k, v := range userRoot.accessFiles {
		expectedRoot.accessFiles[k] = v
	}
	expectedRoot.accessFiles[upspin.PathName(accessPath)] = acc
	expectedRootJSON := toRootJSON(t, &expectedRoot)
	if !bytes.Equal(egcp.PutContents[2], expectedRootJSON) {
		t.Errorf("Expected new root %s, got %s", expectedRootJSON, egcp.PutContents[2])
	}
}

func TestGroupAccessFile(t *testing.T) {
	// There's an access file that gives rights to a Group called family, which contains one user.
	const broUserName = "bro@family.com"
	newRoot := userRoot
	newRoot.accessFiles = make(accessFileDB)
	newRoot.accessFiles[rootAccessFile] = makeAccess(t, rootAccessFile, "r,l,w,c: family, "+userName)
	rootJSON := toRootJSON(t, &newRoot)

	refOfGroupFile := "sha-256 of Group/family"
	groupDir := upspin.DirEntry{
		Name: upspin.PathName(userName + "/Group/family"),
		Location: upspin.Location{
			Reference: upspin.Reference(refOfGroupFile),
			Endpoint:  dir.Location.Endpoint, // Same endpoint as the dir entry itself.
		},
	}
	groupDirJSON := toJSON(t, groupDir)

	groupParentDir := upspin.DirEntry{
		Name: upspin.PathName(userName + "/Group"),
		Metadata: upspin.Metadata{
			Attr: upspin.AttrDirectory,
		},
	}
	groupParentDirJSON := toJSON(t, &groupParentDir)

	// newGroupDir is where the new group file will go when the user puts it. Just the reference changes.
	newRefOfGroupFile := "new sha-256 of newly-put Group/family"
	newGroupDir := groupDir
	newGroupDir.Location.Reference = upspin.Reference(newRefOfGroupFile)
	newGroupDirJSON := toJSON(t, &newGroupDir)

	contentsOfFamilyGroup := broUserName
	newContentsOfFamilyGroup := "sister@family.com" // bro@family.com is dropped!

	// We'll now attempt to have broUserName read a file under userName's tree.
	dirJSON := toJSON(t, dir) // dir is the dirEntry of the file that broUserName will attempt to read

	// Expected success (that is, dir.Get returns the dirJSON entry)
	resp := nettest.NewExpectingResponseWriter(string(dirJSON))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8081/dir/"+pathName, nil)

	// Internally, we look up the root, the Group file and finally the pathName requested. Later, a new group file is
	// put so we lookup its parent and finally we retrieve the new group entry.
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, userName + "/Group/family", pathName, userName + "/Group", userName + "/Group/family"},
		Data: [][]byte{rootJSON, groupDirJSON, dirJSON, groupParentDirJSON, newGroupDirJSON},
	}

	// Setup the directory's store client to return the contents of the Group file.
	d1 := &dummyStore{
		ref:      upspin.Reference(refOfGroupFile),
		contents: []byte(contentsOfFamilyGroup),
	}
	d2 := &dummyStore{
		ref:      upspin.Reference(newRefOfGroupFile),
		contents: []byte(newContentsOfFamilyGroup),
	}
	f, err := factotum.New(serverKeys)
	if err != nil {
		t.Fatal(err)
	}
	// Create a session for broUserName
	session := testauth.NewSessionForTesting(broUserName, false, nil)
	// Create a store factory that returns d1 then d2.
	count := 0
	ds := newDirServer(egcp, f, func(e upspin.Endpoint) (upspin.Store, error) {
		count++
		switch count {
		case 1:
			return d1, nil
		case 2:
			return d2, nil
		}
		return nil, errors.New("invalid")
	})
	ds.dirHandler(session, resp, req)

	// Now Put a new Group with new contents that does not include broUserName and check that if we fetch the file
	// again with access will be denied, because the new definition got picked up (after first being invalidated).

	groupReq := nettest.NewRequest(t, netutil.Post, u("http://localhost:8081/dir/", newGroupDir.Name), newGroupDirJSON)
	resp = nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	ds.dirHandler(dummySess, resp, groupReq) // This is the owner of the file putting the new group file.
	resp.Verify(t)

	// Expected permission denied this time.
	resp = nettest.NewExpectingResponseWriter(`{"error":"DirService: Get: test@foo.com/mydir/myfile.txt: permission denied"}`)
	req = nettest.NewRequest(t, netutil.Get, "http://localhost:8081/dir/"+pathName, nil)

	ds.dirHandler(session, resp, req) // same session: for broUserName
	resp.Verify(t)
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
		dirEntry: upspin.DirEntry{
			Name: upspin.PathName("me@here.com/"),
			Metadata: upspin.Metadata{
				Attr: upspin.AttrDirectory,
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
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/dir/hello@foo.com/bla.txt", []byte("crap data"))

	ds := newTestDirServer(t, &gcptest.DummyGCP{})
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGCPCorruptsData(t *testing.T) {
	dirEntryJSON := toJSON(t, dir)
	rootJSON := toRootJSON(t, &userRoot)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, parentPathName},
		Data: [][]byte{rootJSON, []byte("really bad JSON structure that does not parse")},
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: getmeta: test@foo.com/mydir: json unmarshal failed retrieving metadata: invalid character 'r' looking for beginning of value"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/dir/"+pathName, dirEntryJSON)

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGet(t *testing.T) {
	dirEntryJSON := toJSON(t, dir)
	rootJSON := toRootJSON(t, &userRoot)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, pathName},
		Data: [][]byte{rootJSON, dirEntryJSON},
	}

	resp := nettest.NewExpectingResponseWriter(string(dirEntryJSON))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+pathName, nil)

	ds := newTestDirServer(t, egcp)
	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestGetPermissionDenied(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Get: test@foo.com/mydir/myfile.txt: permission denied"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+pathName, nil)

	ds := newTestDirServer(t, egcp)

	sess := testauth.NewSessionForTesting("sloppyjoe@unauthorized.com", false, nil)
	ds.dirHandler(sess, resp, req)
	resp.Verify(t)
}

func TestDelete(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)
	dirEntryJSON := toJSON(t, dir)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName, pathName},
			Data: [][]byte{rootJSON, dirEntryJSON},
		},
		deletePathExpected: pathName,
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Delete, "http://localhost:8080/dir/"+pathName, nil)

	ds := newTestDirServer(t, lgcp)

	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	if lgcp.listDirCalled {
		t.Errorf("ListDir should not have been called as pathName is not a directory")
	}
	if !lgcp.deleteCalled {
		t.Errorf("Delete should have been called")
	}
}

func TestDeleteDirNotEmpty(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)
	parentPathJSON := toJSON(t, dirParent)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName, parentPathName},
			Data: [][]byte{rootJSON, parentPathJSON},
		},
		prefix:    parentPathName + "/",
		fileNames: []string{pathName}, // pathName is inside parentPathName.
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Delete: test@foo.com/mydir: directory not empty"}`)
	req := nettest.NewRequest(t, netutil.Delete, "http://localhost:8080/dir/"+parentPathName, nil)

	ds := newTestDirServer(t, lgcp)

	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	if !lgcp.listDirCalled {
		t.Errorf("ListDir should have been called as pathName is a directory")
	}
	if lgcp.deleteCalled {
		t.Errorf("Delete should not have been called")
	}
}

func TestDeleteDirPermissionDenied(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName}, // only the root is looked up.
			Data: [][]byte{rootJSON},
		},
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: Delete: test@foo.com/mydir/myfile.txt: permission denied"}`)
	req := nettest.NewRequest(t, netutil.Delete, "http://localhost:8080/dir/"+pathName, nil)

	ds := newTestDirServer(t, lgcp)

	session := testauth.NewSessionForTesting(upspin.UserName("some-random-dude@bozo.com"), false, nil)
	ds.dirHandler(session, resp, req)
	resp.Verify(t)

	if lgcp.listDirCalled {
		t.Errorf("ListDir should not have been called as pathName is not a directory")
	}
	if lgcp.deleteCalled {
		t.Errorf("Delete should not have been called")
	}
}

func TestDeleteAccessFile(t *testing.T) {
	accessDir := upspin.DirEntry{
		Name: rootAccessFile,
		Location: upspin.Location{
			Reference: "some place in store", // We don't need this, but just for completion.
		},
	}
	accessDirJSON := toJSON(t, accessDir)
	// Let's pretend we had a non-default Access file for the root dir.
	accessFile := makeAccess(t, rootAccessFile, "r,w,c: somefolks@domain.com")
	newRoot := userRoot
	newRoot.accessFiles = accessFileDB{rootAccessFile: accessFile}

	rootJSON := toRootJSON(t, &newRoot)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName, rootAccessFile},
			Data: [][]byte{rootJSON, accessDirJSON},
		},
		deletePathExpected: rootAccessFile,
	}

	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Delete, "http://localhost:8080/dir/"+rootAccessFile, nil)

	ds := newTestDirServer(t, lgcp)

	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	// Verify we put a new root with a plain vanilla Access file.
	if len(lgcp.PutRef) != 1 {
		t.Fatalf("Expected one Put, got %d", len(lgcp.PutRef))
	}
	if lgcp.PutRef[0] != userName {
		t.Errorf("Expected a write to the root (%s/), wrote to %s instead", userName, lgcp.PutRef[0])
	}
	savedRoot := lgcp.PutContents[0]
	expectedRoot := toRootJSON(t, &userRoot)
	if !bytes.Equal(savedRoot, expectedRoot) {
		t.Errorf("Expected to save root contents %s, saved contents %s instead", expectedRoot, savedRoot)
	}
	// Verify we deleted the Access file
	if !lgcp.deleteCalled {
		t.Fatal("Delete on GCP was not called")
	}
}

func TestDeleteGroupFile(t *testing.T) {
	// There's an access file that gives rights to a Group called family, which contains one user.
	const broUserName = "bro@family.com"
	newRoot := userRoot
	newRoot.accessFiles = make(accessFileDB)
	newRoot.accessFiles[rootAccessFile] = makeAccess(t, rootAccessFile, "r,l,w,c: family, "+userName)
	rootJSON := toRootJSON(t, &newRoot)

	dirJSON := toJSON(t, dir)

	groupPathName := upspin.PathName(userName + "/Group/family")
	access.AddGroup(groupPathName, []byte(broUserName))

	refOfGroupFile := "sha-256 of Group/family"
	groupDir := upspin.DirEntry{
		Name: groupPathName,
		Location: upspin.Location{
			Reference: upspin.Reference(refOfGroupFile),
			Endpoint:  dir.Location.Endpoint, // Same endpoint as the dir entry itself.
		},
	}
	groupDirJSON := toJSON(t, groupDir)

	lgcp := &listGCP{
		ExpectDownloadCapturePutGCP: gcptest.ExpectDownloadCapturePutGCP{
			Ref:  []string{userName, pathName, string(groupPathName)},
			Data: [][]byte{rootJSON, dirJSON, groupDirJSON},
		},
		deletePathExpected: string(groupPathName),
	}

	// Verify that bro@family.com has access.
	resp := nettest.NewExpectingResponseWriter(string(dirJSON))
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+pathName, nil)

	ds := newTestDirServer(t, lgcp)

	broSess := testauth.NewSessionForTesting(upspin.UserName(broUserName), false, nil)
	ds.dirHandler(broSess, resp, req)
	resp.Verify(t)

	// Now the owner deletes the group file.
	resp = nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req = nettest.NewRequest(t, netutil.Delete, u("http://localhost:8080/dir/", groupPathName), nil)

	ds.dirHandler(dummySess, resp, req)
	resp.Verify(t)

	if !lgcp.deleteCalled {
		t.Errorf("Expected delete to be called on %s", groupPathName)
	}

	// And now the session for bro (broSess) can no longer read it.
	// TODO: this error message is not helpful. It should contain permission denied plus the path
	// to the missing Group file.
	resp = nettest.NewExpectingResponseWriter(`{"error":"DirService: Get: download: pathname not found"}`)
	req = nettest.NewRequest(t, netutil.Get, "http://localhost:8080/dir/"+pathName, nil)
	ds.dirHandler(broSess, resp, req)
	resp.Verify(t)
}

func TestWhichAccessImplicitAtRoot(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"Access":""}`)
	req := nettest.NewRequest(t, netutil.Get, u("http://localhost:8080/whichaccess/", pathName), nil)

	// The Access file at the root really exists.
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.whichAccessHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestWhichAccess(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)
	accessJSON := toJSON(t, upspin.DirEntry{
		Name: rootAccessFile,
	})

	resp := nettest.NewExpectingResponseWriter(fmt.Sprintf(`{"Access":"%s"}`, rootAccessFile))
	req := nettest.NewRequest(t, netutil.Get, u("http://localhost:8080/whichaccess/", pathName), nil)

	// The Access file at the root really exists.
	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName, rootAccessFile},
		Data: [][]byte{rootJSON, accessJSON},
	}

	ds := newTestDirServer(t, egcp)
	ds.whichAccessHandler(dummySess, resp, req)
	resp.Verify(t)
}

func TestWhichAccessPermissionDenied(t *testing.T) {
	rootJSON := toRootJSON(t, &userRoot)

	resp := nettest.NewExpectingResponseWriter(`{"error":"DirService: WhichAccess: test@foo.com/mydir/myfile.txt: permission denied"}`)
	req := nettest.NewRequest(t, netutil.Get, u("http://localhost:8080/whichaccess/", pathName), nil)

	egcp := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:  []string{userName},
		Data: [][]byte{rootJSON},
	}

	ds := newTestDirServer(t, egcp)
	session := testauth.NewSessionForTesting(upspin.UserName("somerandomguy@a.co"), false, nil)
	ds.whichAccessHandler(session, resp, req)
	resp.Verify(t)
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

func newTestDirServer(t *testing.T, gcp gcp.GCP) *dirServer {
	f, err := factotum.New(serverKeys)
	if err != nil {
		t.Fatal(err)
	}
	return newDirServer(gcp, f, nil)
}

// u (short for URL) is a helper function to join the pieces of a request URL.
func u(baseURL string, pathName upspin.PathName) string {
	return baseURL + string(pathName)
}

// listGCP is an ExpectDownloadCapturePutGCP that returns a slice of fileNames
// if a call to ListPrefix or ListDir matches the expected prefix or dir.
type listGCP struct {
	gcptest.ExpectDownloadCapturePutGCP
	prefix             string
	fileNames          []string
	listPrefixCalled   bool
	listDirCalled      bool
	deletePathExpected string
	deleteCalled       bool
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

func (l *listGCP) Delete(path string) error {
	l.deleteCalled = true
	if path == l.deletePathExpected {
		return nil
	}
	return errors.New("Not found")
}

type dummyStore struct {
	ref      upspin.Reference
	contents []byte
}

var _ upspin.Store = (*dummyStore)(nil)

func (d *dummyStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	if ref == d.ref {
		return d.contents, nil, nil
	}
	return nil, nil, errors.New("not found")
}
func (d *dummyStore) Put(data []byte) (upspin.Reference, error) {
	panic("unimplemented")
}
func (d *dummyStore) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	panic("unimplemented")
}
func (d *dummyStore) Ping() bool {
	return true
}
func (d *dummyStore) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}
func (d *dummyStore) ServerUserName() string {
	panic("unimplemented")
}
func (d *dummyStore) Configure(options ...string) error {
	panic("unimplemented")
}
func (d *dummyStore) Delete(ref upspin.Reference) error {
	panic("unimplemented")
}
func (d *dummyStore) Close() {
}
func (d *dummyStore) Authenticate(*upspin.Context) error {
	return nil
}
