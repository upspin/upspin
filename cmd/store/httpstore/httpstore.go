package main

import (
	"flag"
	"fmt"
	"net/http"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/auth/httpauth"
	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/jsonmsg"
	"upspin.googlesource.com/upspin.git/cmd/store"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/log"
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
	port                  = flag.Int("port", 8080, "TCP port to serve HTTP requests.")
	noAuth                = flag.Bool("noauth", false, "Disable authentication.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

// httpStoreServer wraps a storeServer with methods for serving HTTP requests.
type httpStoreServer struct {
	store *store.Server
}

// Handler for receiving file put requests (i.e. storing new blobs).
// Requests must contain a 'file' form entry.
func (s *httpStoreServer) putHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "PUT" {
		netutil.SendJSONErrorString(w, store.ServerName+"post or put request expected")
		return
	}
	r.ParseMultipartForm(multiPartMemoryBuffer)
	file, _, err := r.FormFile("file")
	if err != nil {
		netutil.SendJSONError(w, store.ServerName, fmt.Errorf("Put: %s", err))
		return
	}
	defer file.Close()

	ref, err := s.store.InnerPut(sess.User(), file)
	if err != nil {
		netutil.SendJSONErrorString(w, err.Error())
		return
	}

	// Answer something sensible to the client.
	log.Printf("Put: %s: %s", sess.User(), ref)
	jsonmsg.SendReferenceResponse(ref, w)
}

func (s *httpStoreServer) getHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONError(w, store.ServerName, store.ErrInvalidRef)
		return
	}
	log.Printf("Looking up ref %s in cache for user %s", ref, sess.User())

	file, location, err := s.store.InnerGet(sess.User(), upspin.Reference(ref))
	if err != nil {
		netutil.SendJSONErrorString(w, err.Error())
		return
	}
	if file != nil {
		// Ref is in the local cache. Send the file and be done.
		log.Printf("ref %s is in local cache. Returning it as file: %s", ref, file.Name())
		defer file.Close()
		http.ServeFile(w, r, file.Name())
		return
	}

	netutil.SendJSONReply(w, location)
}

func (s *httpStoreServer) deleteHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	if ref == "" {
		netutil.SendJSONError(w, store.ServerName, store.ErrInvalidRef)
		return
	}
	// TODO: move this to DELETE.
	if r.Method != "POST" {
		netutil.SendJSONErrorString(w, store.ServerName+"Delete only accepts POST HTTP requests")
		return
	}
	err := s.store.Delete(sess.User(), upspin.Reference(ref))
	if err != nil {
		netutil.SendJSONError(w, store.ServerName, err)
		return
	}

	netutil.SendJSONErrorString(w, "success")
}

func main() {
	flag.Parse()

	log.Connect("google.com:"+*projectID, *bucketName)

	store := &store.Server{
		CloudClient: gcp.New(*projectID, *bucketName, gcp.PublicRead),
		FileCache:   cache.NewFileCache(*tempDir),
	}

	storeHTTP := &httpStoreServer{
		store: store,
	}

	ah := httpauth.NewHandler(&auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	http.HandleFunc("/put", ah.Handle(storeHTTP.putHandler))
	http.HandleFunc("/get", ah.Handle(storeHTTP.getHandler))
	http.HandleFunc("/delete", ah.Handle(storeHTTP.deleteHandler))

	if *sslCertificateFile != "" && *sslCertificateKeyFile != "" {
		server, err := httpauth.NewHTTPSecureServer(*port, *sslCertificateFile, *sslCertificateKeyFile)
		if err != nil {
			log.Error.Fatal(err)
		}
		log.Println("Starting HTTPS server with SSL.")
		log.Error.Fatal(server.ListenAndServeTLS(*sslCertificateFile, *sslCertificateKeyFile))
	} else {
		log.Println("Not using SSL certificate. Starting regular HTTP server.")
		log.Error.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
	}
}
