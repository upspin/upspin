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
	MaxBuffSizePerReq = 1 << 20 // 1MB max buff size per request

	// TODO(ehg): review these numbers. They're are random guesses for now.
	SignatureMinLen  = 32
	SignatureMaxLen  = 128
	WrappedKeyMinLen = 512
	WrappedKeyMaxLen = 2048
)

var (
	projectId   = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName  = flag.String("bucket", "g-upspin-directory", "The name of an existing bucket within the project.")
	cloudClient *gcp.GCP

	CloudEntryNotFound = DirEntryError{"not found"}
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
func verifyDirEntry(dirEntry upspin.DirEntry) (parsedPath path.Parsed, err error) {
	// Can we parse this path?
	parsedPath, err = path.Parse(dirEntry.Name)
	if err != nil {
		return
	}
	// Checks the protocol
	switch dirEntry.Location.Reference.Protocol {
	case upspin.HTTP:
		err = verifyUrl(dirEntry.Location.Reference.Key)
		if err != nil {
			return
		}
	case upspin.EllipticalEric, upspin.Debug:
		log.Println("Not implemented, but ok for now")
	default:
		err = DirEntryError{fmt.Sprintf("unknown protocol: %v", dirEntry.Location.Reference.Protocol)}
		return
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
	l := len(meta.Signature)
	if l > SignatureMaxLen || l < SignatureMinLen {
		return DirEntryError{"signature is invalid"}
	}
	if len(meta.WrappedKeys) < 1 {
		return DirEntryError{"need at least one wrapped key"}
	}
	for _, k := range meta.WrappedKeys {
		l = len(k.Encrypted)
		if l > WrappedKeyMaxLen || l < WrappedKeyMinLen {
			return DirEntryError{"invalid wrapped key"}
		}
	}
	return nil
}

// verifyUrl checks that a url is minimally valid.
func verifyUrl(urlStr string) error {
	_, err := url.Parse(urlStr)
	return err
}

// putHandler handlers file put requests, for storing or updating
// metadata information.
func putHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("In handler for /put")
	if r.Method != "POST" && r.Method != "PUT" {
		netutil.SendJSONErrorString(w, "/put only handles POST http requests")
		return
	}
	buf := netutil.BufferRequest(w, r, MaxBuffSizePerReq) // closes r.Body
	if buf == nil {
		// Request was invalid and was closed. Nothing else to do.
		return
	}
	dirEntry := upspin.DirEntry{}
	err := json.Unmarshal(buf, &dirEntry)
	if err != nil {
		netutil.SendJSONError(w, "error unmarshaling:", err)
		return
	}
	err = tryCreateDirEntry(dirEntry)
	if err != nil {
		netutil.SendJSONError(w, "", err)
		return
	}
	netutil.SendJSONReply(w, `{error:"Success"}`)
}

// tryCreateDirEntry will attempt to write a new dirEntry to the back
// end, provided several checks have passed first.
func tryCreateDirEntry(dirEntry upspin.DirEntry) error {
	parsedPath, err := verifyDirEntry(dirEntry)
	if err != nil {
		return DirEntryError{fmt.Sprintf("dir entry verification failed:%v", err)}
	}
	// All checks passed so far. Now go put the object into GCE.
	fmt.Printf("Got valid dir entry for path: %v\n", parsedPath)

	// Verifies that we can write to the parent directory.
	err = verifyParentWritable(parsedPath)
	if err != nil {
		return DirEntryError{"path is not writable"}
	}

	// Before we can create this entry, we verify that we're not
	// trying to overwrite a file with a directory or a directory
	// with a file. That's probably not what the user wanted
	// anyway.
	path := parsedPath.String()
	otherDir, err := getMeta(path)
	if err != CloudEntryNotFound {
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

// verifyParentWritable checks whether the parent dir of a path can be written to.
func verifyParentWritable(path path.Parsed) error {
	l := len(path.Elems)
	if l == 0 {
		// The root is a writable directory (modulo ACLs).
		return nil
	}
	if l == 1 {
		// Anything under the root is writable (modulo ACLs).
		return nil
	}
	// Check that the last entry before the one we're trying to
	// create is already a directory.
	parentDirPath := path.First(l - 1)
	dirEntry, err := getMeta(parentDirPath.String())
	if err != nil {
		return err
	}
	if !dirEntry.Metadata.IsDir {
		return DirEntryError{"parent directory given is not a directory"}
	}
	return nil
}

// getMeta returns the metadata for the given path or an error.
func getMeta(path string) (upspin.DirEntry, error) {
	log.Printf("Looking up dir entry %s on storage backend\n", path)
	var dirEntry upspin.DirEntry
	buf, err := getCloudBytes(path)
	if err != nil {
		return dirEntry, err
	}
	err = json.Unmarshal(buf, &dirEntry)
	if err != nil {
		return dirEntry, DirEntryError{fmt.Sprintf("json unmarshal failed retrieving metadata:%v", err)}
	}
	return dirEntry, nil
}

// putMeta forcibly writes the given dirEntry to the path on the
// backend without checking anything. Returns an error if the cloud
// operation failed.
func putMeta(path string, dirEntry upspin.DirEntry) error {
	jsonBuf, err := json.Marshal(dirEntry)
	if err != nil {
		return DirEntryError{fmt.Sprintf("conversion to json failed:%v", err)}
	}
	_, err = cloudClient.Put(path, jsonBuf)
	return err
}

// getCloudBytes fetches the exact full path given (including user
// name) from the storage backend. It returns the bytes if found or an
// error otherwise.
func getCloudBytes(path string) ([]byte, error) {
	var empty []byte
	link, err := cloudClient.Get(path)
	if err != nil {
		return empty, CloudEntryNotFound
	}
	// Now use the link to retrieve the metadata.
	resp, err := http.Get(link)
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return empty, DirEntryError{fmt.Sprintf("error reading cloud:%v", err)}
	}
	return buf, nil
}

func configureCloudClient(projectId, bucketName string) {
	cloudClient = gcp.New(projectId, bucketName, gcp.DefaultWriteACL)
}

func main() {
	flag.Parse()
	configureCloudClient(*projectId, *bucketName)
	http.HandleFunc("/put", putHandler)
	// TODO /get, for retrieving information about a file or dir.

	log.Println("Starting server...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
