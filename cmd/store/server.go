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
	MultiPartMemoryBuffer int64 = 10 << 20 // 10MB buffer for file transfers
)

var (
	projectId   = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName  = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
	tempDir     = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default")
	cloudClient *gcp.GCP
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
		if _, err := cloudClient.PutLocalFile(fileCache.GetFileLocation(ref), ref); err == nil {
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
	location.Transport = "HTTP" // TODO(edpin): specify or use a constant
	// Leave location.NetAddr empty for now (does it make sense to
	// be the NetAddr of the GCE storage server, if we're not yet
	// providing the user with a direct link?)
	fmt.Printf("Replying to client with location: %v. Ref:%v\n", location, ref)
	netutil.SendJSONReply(w, location)
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONErrorString(w, "Invalid empty 'ref'")
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
	link, err := cloudClient.Get(ref)
	if err != nil {
		netutil.SendJSONError(w, "get error:", err)
		return
	}

	location := upspin.Location{}
	location.Reference.Key = link
	fmt.Printf("Got link: %v\n", link)
	// Do we need to be precise and say HTTPS here? Probably not,
	// as the Key already specifies "https://" to indicate the
	// right packing.
	location.Reference.Packing = upspin.HTTP
	// Leave location.NetAddr empty for now. It could probably be
	// what "www.googleapis.com" resolves to at this moment, but
	// the Key contains all info needed for clients to find it
	// ("https://www.googleapis.com...")
	netutil.SendJSONReply(w, location)
}

func main() {
	flag.Parse()
	cloudClient = gcp.New(*projectId, *bucketName, gcp.DefaultWriteACL)
	fileCache = cache.NewFileCache(*tempDir)
	http.HandleFunc("/put", putHandler)
	http.HandleFunc("/get", getHandler)
	// TODO(edpin): Implement delete.
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal(err)
	}
}
