package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"upspin.googlesource.com/upspin.git/store/cache"
	"upspin.googlesource.com/upspin.git/store/cloud"
)

const (
	MultiPartMemoryBuffer int64  = 10 << 20 // 10MB buffer for file transfers
	DefaultTempDir        string = ""       // Use the system's default
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
			// Remove the locally-cached entry. This is safe to do because FileCache is thread safe.
			fileCache.Purge(ref)
		}
	}(ref)

	// TODO(edpin): some cache management is necessary to remove local copies when the cache is full.

	// Answer something sensible to the client.
	w.Write([]byte(fmt.Sprintf("{ref=%s, error='ok'}", url.QueryEscape(ref))))
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	blob := r.FormValue("ref")
	if blob == "" {
		w.Write([]byte("Invalid empty blob"))
		return
	}
	fmt.Println("Trying to get blob: %v", blob)

	// TODO(edpin): check whether the blob is already local. If so, just return it.

	link, err := cloudClient.GetBlob(blob)
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}
	w.Write([]byte(fmt.Sprintf("Getting blob: %v, which is in: %v", blob, link)))
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
