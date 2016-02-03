package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"flag"
	"log"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	maxBuffSizePerReq = 1 << 20 // 1MB max buff size per request
)

var (
	projectId   = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName  = flag.String("bucket", "g-upspin-directory", "The name of an existing bucket within the project.")
	cloudClient *gcp.GCP

	errEntryNotFound = DirEntryError{"pathname not found"}
)

type DirEntryError struct {
	error string
}

func (d DirEntryError) Error() string {
	return d.error
}

// verifyDirEntry checks that the dirEntry given by the user is
// minimally valid (we can't enforce a crypto verification here, that
// can only be done in the client). It returns a parsed path or an
// error if one occurred.
func verifyDirEntry(dirEntry *upspin.DirEntry) (parsedPath path.Parsed, err error) {
	// Can we parse this path?
	parsedPath, err = path.Parse(dirEntry.Name)
	if err != nil {
		return
	}
	// Checks the transport
	switch dirEntry.Location.Endpoint.Transport {
	case upspin.HTTP:
		err = verifyUrl(dirEntry.Location.Reference.Key)
		if err != nil {
			return
		}
	default:
		log.Println("Not implemented, but ok for now")

	}
	// Checks the metadata
	return parsedPath, verifyMetadata(dirEntry.Metadata)
}

// verifyMetadata checks that the metadata portion of the DirEntry is
// minimally valid.
func verifyMetadata(meta upspin.Metadata) error {
	if meta.Sequence < 0 {
		return DirEntryError{"invalid sequence number"}
	}
	return nil
}

// verifyUrl checks that a url is minimally valid.
func verifyUrl(urlStr string) error {
	_, err := url.Parse(urlStr)
	return err
}

// putHandler handles file put requests, for storing or updating
// metadata information.
func putHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("In handler for /put")
	if r.Method != "POST" && r.Method != "PUT" {
		netutil.SendJSONErrorString(w, "/put only handles POST http requests")
		return
	}
	buf := netutil.BufferRequest(w, r, maxBuffSizePerReq) // closes r.Body
	if buf == nil {
		// Request was invalid and was closed. Nothing else to do.
		return
	}
	dirEntry := &upspin.DirEntry{}
	err := json.Unmarshal(buf, dirEntry)
	if err != nil {
		netutil.SendJSONError(w, "error unmarshaling:", err)
		return
	}
	// TODO: verify ACLs before applying put.
	err = createDirEntry(dirEntry)
	if err != nil {
		netutil.SendJSONError(w, "", err)
		return
	}
	netutil.SendJSONReply(w, `{"error":"Success"}`)
}

// createDirEntry will attempt to write a new dirEntry to the back
// end, provided several checks have passed first.
func createDirEntry(dirEntry *upspin.DirEntry) error {
	parsedPath, err := verifyDirEntry(dirEntry)
	if err != nil {
		return DirEntryError{fmt.Sprintf("dir entry verification failed: %v", err)}
	}
	// All checks passed so far. Now go put the object into GCE.
	fmt.Printf("Got valid dir entry for path: %v\n", parsedPath)

	err = verifyParentWritable(parsedPath)
	if err != nil {
		return DirEntryError{"path is not writable"}
	}

	// Before we can create this entry, we verify that we're not
	// trying to overwrite a file with a directory or a directory
	// with a file. That's probably not what the user wanted
	// anyway.
	path := parsedPath.Path()
	otherDir, err := getMeta(path)
	if err != errEntryNotFound {
		if otherDir.Metadata.IsDir && !dirEntry.Metadata.IsDir {
			return DirEntryError{"Overwriting dir with file"}
		}
		if !otherDir.Metadata.IsDir && dirEntry.Metadata.IsDir {
			return DirEntryError{"Overwriting file with dir"}
		}
	}

	// Writes the entry
	return putMeta(path, dirEntry)
}

// verifyParentWritable returns an error if the parent dir of a path cannot be written to.
func verifyParentWritable(path path.Parsed) error {
	l := len(path.Elems)
	if l <= 1 {
		// The root is a writable directory (modulo ACLs).
		return nil
	}
	// Check that the last entry before the one we're trying to
	// create is already a directory.
	dirEntry, err := getMeta(path.Drop(1).Path())
	if err != nil {
		return err
	}
	if !dirEntry.Metadata.IsDir {
		return DirEntryError{"parent directory given is not a directory"}
	}
	return nil
}

// getMeta returns the metadata for the given path.
func getMeta(path upspin.PathName) (*upspin.DirEntry, error) {
	log.Printf("Looking up dir entry %q on storage backend\n", path)
	var dirEntry upspin.DirEntry
	buf, err := getCloudBytes(path)
	if err != nil {
		return &dirEntry, err
	}
	err = json.Unmarshal(buf, &dirEntry)
	if err != nil {
		return &dirEntry, DirEntryError{fmt.Sprintf("json unmarshal failed retrieving metadata: %v", err)}
	}
	return &dirEntry, nil
}

// putMeta forcibly writes the given dirEntry to the path on the
// backend without checking anything.
func putMeta(path upspin.PathName, dirEntry *upspin.DirEntry) error {
	jsonBuf, err := json.Marshal(dirEntry)
	if err != nil {
		return DirEntryError{fmt.Sprintf("conversion to json failed: %v", err)}
	}
	_, err = cloudClient.Put(string(path), jsonBuf)
	return err
}

// getCloudBytes fetches the path from the storage backend.
func getCloudBytes(path upspin.PathName) ([]byte, error) {
	link, err := cloudClient.Get(string(path))
	if err != nil {
		return nil, errEntryNotFound
	}
	// Now use the link to retrieve the metadata.
	resp, err := http.Get(link)
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, DirEntryError{fmt.Sprintf("error reading cloud: %v", err)}
	}
	return buf, nil
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil {
		// This is so bad it's probably a panic at this point. URL should never be nil.
		netutil.SendJSONErrorString(w, "server error: invalid URL")
		return
	}
	context := "get: "
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	pathName := r.FormValue("pathname")
	if pathName == "" {
		netutil.SendJSONErrorString(w, "missing pathname in request")
		return
	}
	dirEntry, err := getMeta(upspin.PathName(pathName))
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// We have a dirEntry. Marshal it and send it back.
	// TODO: verify ACLs before replying.
	dirEntryJson, err := json.Marshal(dirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
	}

	netutil.SendJSONReply(w, dirEntryJson)
}

func configureCloudClient(projectId, bucketName string) {
	cloudClient = gcp.New(projectId, bucketName, gcp.DefaultWriteACL)
}

func main() {
	flag.Parse()
	configureCloudClient(*projectId, *bucketName)
	http.HandleFunc("/put", putHandler)
	http.HandleFunc("/get", getHandler)
	log.Println("Starting server...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
