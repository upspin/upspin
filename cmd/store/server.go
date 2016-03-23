package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cmd/serverauth"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/key/sha256key"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

const (
	multiPartMemoryBuffer int64 = 10 << 20 // 10MB buffer for file transfers
)

var (
	projectID             = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName            = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
	tempDir               = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default.")
	port                  = flag.Int("port", 8080, "TCP port to serve.")
	noAuth                = flag.Bool("noauth", false, "Disable authentication.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
	context               = "StoreService: "
	errInvalidRef         = errors.New("invalid reference")
)

type storeServer struct {
	cloudClient gcp.GCP
	fileCache   *cache.FileCache
}

// Handler for receiving file put requests (i.e. storing new blobs).
// Requests must contain a 'file' form entry.
func (s *storeServer) putHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "PUT" {
		netutil.SendJSONErrorString(w, "post or put request expected")
		return
	}
	r.ParseMultipartForm(multiPartMemoryBuffer)
	file, _, err := r.FormFile("file")
	if err != nil {
		netutil.SendJSONError(w, context, fmt.Errorf("Put: %s", err))
		return
	}
	defer file.Close()
	sha := sha256key.NewShaReader(file)
	initialRef := s.fileCache.RandomRef()
	err = s.fileCache.Put(initialRef, sha)
	if err != nil {
		netutil.SendJSONError(w, context, fmt.Errorf("Put: %s", err))
		return
	}
	// Figure out the appropriate reference for this blob
	ref := sha.EncodedSum()

	// Rename it in the cache
	s.fileCache.Rename(ref, initialRef)

	// Now go store it in the cloud.
	go func() {
		if _, err := s.cloudClient.PutLocalFile(s.fileCache.GetFileLocation(ref), ref); err == nil {
			// Remove the locally-cached entry so we never
			// keep files locally, as we're a tiny server
			// compared with our much better-provisioned
			// storage backend.  This is safe to do
			// because FileCache is thread safe.
			s.fileCache.Purge(ref)
		}
	}()

	// Answer something sensible to the client.
	refMessage := &struct {
		Ref string
	}{
		Ref: ref,
	}
	fmt.Printf("Put: %s: %v\n", sess.User(), refMessage)
	netutil.SendJSONReply(w, refMessage)
}

func (s *storeServer) getHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONError(w, context, errInvalidRef)
		return
	}
	fmt.Printf("Looking up ref %s in cache for user %s\n", ref, sess.User())

	file, err := s.fileCache.OpenRefForRead(ref)
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		fmt.Printf("ref %s is in local cache. Returning it as file: %s\n", ref, file.Name())
		defer file.Close()
		http.ServeFile(w, r, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	link, err := s.cloudClient.Get(ref)
	if err != nil {
		netutil.SendJSONError(w, context, fmt.Errorf("get: %s", err))
		return
	}
	// GCP should return an http link
	if !strings.HasPrefix(link, "http") {
		errMsg := fmt.Sprintf("get: invalid link returned from GCP: %s", link)
		netutil.SendJSONError(w, context, errors.New(errMsg))
		log.Println(errMsg)
		return
	}

	location := upspin.Location{}
	location.Reference = upspin.Reference(link)
	location.Endpoint.Transport = upspin.GCP // Go fetch using the provided link.
	fmt.Printf("Ref %s returned as link: %s\n", ref, link)
	netutil.SendJSONReply(w, location)
}

func (s *storeServer) deleteHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONError(w, context, errInvalidRef)
		return
	}
	if r.Method != "POST" {
		netutil.SendJSONErrorString(w, context+"Delete only accepts POST HTTP requests")
		return
	}
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.cloudClient.Delete(ref)
	if err != nil {
		netutil.SendJSONError(w, context, fmt.Errorf("delete: %s: %s", ref, err))
		return
	}
	fmt.Printf("Delete: %s: Success\n", ref)
	netutil.SendJSONErrorString(w, "success")
}

func main() {
	flag.Parse()
	ss := &storeServer{
		cloudClient: gcp.New(*projectID, *bucketName, gcp.PublicRead),
		fileCache:   cache.NewFileCache(*tempDir),
	}

	ah := auth.NewHandler(&auth.Config{
		Lookup: serverauth.PublicUserLookupService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	http.HandleFunc("/put", ah.Handle(ss.putHandler))
	http.HandleFunc("/get", ah.Handle(ss.getHandler))
	http.HandleFunc("/delete", ah.Handle(ss.deleteHandler))

	if *sslCertificateFile != "" && *sslCertificateKeyFile != "" {
		server, err := serverauth.NewSecureServer(*port, *sslCertificateFile, *sslCertificateKeyFile)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("Starting HTTPS server with SSL.")
		log.Fatal(server.ListenAndServeTLS(*sslCertificateFile, *sslCertificateKeyFile))
	} else {
		log.Println("Not using SSL certificate. Starting regular HTTP server.")
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
	}
}
