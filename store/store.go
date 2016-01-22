package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"

	"upspin.googlesource.com/upspin.git/store/cache"
	"upspin.googlesource.com/upspin.git/store/cloud"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	MultiPartMemoryBuffer int64 = 10 << 20 // 10MB buffer for file transfers
)

var (
	projectId   = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName  = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
	tempDir     = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default")
	cloudClient *cloud.Cloud
	fileCache   *cache.FileCache
)

// Handler for receiving file put requests (i.e. storing new blobs).
// Requests must contain a 'file' form entry.
func putHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("method:", r.Method)
	if r.Method != "POST" && r.Method != "PUT" {
		log.Fatal("Only handles PUT/POST http requests")
	}
	r.ParseMultipartForm(MultiPartMemoryBuffer)
	file, _, err := r.FormFile("file")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()
	sha := NewShaReader(file)
	initialRef := fileCache.RandomRef()
	err = fileCache.Put(initialRef, sha)
	if err != nil {
		// TODO(edpin): handle this
		panic("cache failed")
	}
	// Figure out the appropriate reference for this blob
	ref := sha.EncodedSum()

	// Rename it in the cache
	fileCache.Rename(ref, initialRef)

	// Now go store it in the cloud.
	go func(ref string) {
		if _, err := cloudClient.PutBlob(fileCache.GetFileLocation(ref), ref); err == nil {
			// Remove the locally-cached entry so we never
			// keep files locally, as we're a tiny server
			// compared with our much better-provisioned
			// storage backend.  This is safe to do
			// because FileCache is thread safe.
			fileCache.Purge(ref)
		}
	}(ref)

	// Answer something sensible to the client.
	location := upspin.Location{}
	location.Reference.Key = ref
	location.Reference.Protocol = upspin.HTTP
	// Leave location.NetAddr empty for now (does it make sense to
	// be the NetAddr of the GCE storage server, if we're not yet
	// providing the user with a direct link?)
	fmt.Printf("Replying to client with location: %v. Ref:%v\n", location, ref)
	sendJSONReply(w, location)
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		sendJSONErrorString(w, "Invalid empty 'ref'")
		return
	}
	fmt.Printf("Trying to get ref: %v\n", ref)

	file, err := fileCache.OpenRefForRead(ref)
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		fmt.Printf("ref %v is in local cache. Returning it as file: %v\n", ref, file.Name())
		defer file.Close()
		http.ServeFile(w, r, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	fmt.Printf("Looking up on storage backend...\n")
	link, err := cloudClient.GetBlob(ref)
	if err != nil {
		sendJSONError(w, err)
		return
	}

	location := upspin.Location{}
	location.Reference.Key = link
	fmt.Printf("Got link: %v\n", link)
	// Do we need to be precise and say HTTPS here? Probably not,
	// as the Key already specifies "https://" to indicate the
	// right protocol.
	location.Reference.Protocol = upspin.HTTP
	// Leave location.NetAddr empty for now. It could probably be
	// what "www.googleapis.com" resolves to at this moment, but
	// the Key contains all info needed for clients to find it
	// ("https://www.googleapis.com...")
	sendJSONReply(w, location)
}

func sendJSONError(w http.ResponseWriter, error error) {
	sendJSONErrorString(w, error.Error())
}

func sendJSONErrorString(w http.ResponseWriter, error string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf("{error='%s'}", error)))
}

// sendJSONReply encodes a reply and sends it out on w as a JSON
// object. Make sure the reply type, if it's a struct (which it most
// likely will be) has *public* fields or nothing will be sent (just
// '{}').
func sendJSONReply(w http.ResponseWriter, reply interface{}) {
	js, err := json.Marshal(reply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(js)
}

func main() {
	flag.Parse()
	cloudClient = cloud.New(*projectId, *bucketName)
	fileCache = cache.NewFileCache(*tempDir)
	http.HandleFunc("/put", putHandler)
	http.HandleFunc("/get", getHandler)
	// TODO(edpin): Implement delete.
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal(err)
	}
}
