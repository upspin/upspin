package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	multiPartMemoryBuffer int64 = 10 << 20 // 10MB buffer for file transfers

	invalidRefError = "invalid ref"
)

var (
	projectId  = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
	tempDir    = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default")
)

type StoreServer struct {
	cloudClient gcp.Interface
	fileCache   *cache.FileCache
}

// Handler for receiving file put requests (i.e. storing new blobs).
// Requests must contain a 'file' form entry.
func (s *StoreServer) putHandler(w http.ResponseWriter, r *http.Request) {
	const op = "putHandler: "
	if r.Method != "POST" && r.Method != "PUT" {
		netutil.SendJSONErrorString(w, "post or put request expected")
		return
	}
	r.ParseMultipartForm(multiPartMemoryBuffer)
	file, _, err := r.FormFile("file")
	if err != nil {
		netutil.SendJSONError(w, op, err)
		return
	}
	defer file.Close()
	sha := NewShaReader(file)
	initialRef := s.fileCache.RandomRef()
	err = s.fileCache.Put(initialRef, sha)
	if err != nil {
		netutil.SendJSONError(w, op, err)
		return
	}
	// Figure out the appropriate reference for this blob
	ref := sha.EncodedSum()

	// Rename it in the cache
	s.fileCache.Rename(ref, initialRef)

	// Now go store it in the cloud.
	go func(ref string) {
		if _, err := s.cloudClient.PutLocalFile(s.fileCache.GetFileLocation(ref), ref); err == nil {
			// Remove the locally-cached entry so we never
			// keep files locally, as we're a tiny server
			// compared with our much better-provisioned
			// storage backend.  This is safe to do
			// because FileCache is thread safe.
			s.fileCache.Purge(ref)
		}
	}(ref)

	// Answer something sensible to the client.
	keyStruct := &struct {
		Key string
	}{
		Key: ref,
	}
	log.Printf("Replying to client with:%v\n", keyStruct)
	netutil.SendJSONReply(w, keyStruct)
}

func (s *StoreServer) getHandler(w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONErrorString(w, invalidRefError)
		return
	}
	log.Printf("Trying to get ref: %v\n", ref)

	file, err := s.fileCache.OpenRefForRead(ref)
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		log.Printf("ref %v is in local cache. Returning it as file: %v\n", ref, file.Name())
		defer file.Close()
		http.ServeFile(w, r, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	log.Printf("Looking up on storage backend...\n")
	link, err := s.cloudClient.Get(ref)
	if err != nil {
		netutil.SendJSONError(w, "get error:", err)
		return
	}

	location := upspin.Location{}
	location.Reference.Key = link
	location.Endpoint.Transport = upspin.HTTP // Go fetch using the provided link.
	log.Printf("Got link: %v\n", link)
	netutil.SendJSONReply(w, location)
}

func (s *StoreServer) deleteHandler(w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONErrorString(w, invalidRefError)
		return
	}
	if r.Method != "POST" {
		netutil.SendJSONErrorString(w, "Delete only accepts POST HTTP requests")
		return
	}
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.cloudClient.Delete(ref)
	if err != nil {
		netutil.SendJSONError(w, fmt.Sprintf("delete %v: ", ref), err)
		return
	}
	netutil.SendJSONErrorString(w, "success")
}

func main() {
	flag.Parse()
	ss := &StoreServer{
		cloudClient: gcp.New(*projectId, *bucketName, gcp.DefaultWriteACL),
		fileCache:   cache.NewFileCache(*tempDir),
	}
	http.HandleFunc("/put", ss.putHandler)
	http.HandleFunc("/get", ss.getHandler)
	http.HandleFunc("/delete", ss.deleteHandler)
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal(err)
	}
}
