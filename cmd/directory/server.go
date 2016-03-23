package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cmd/auth"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

const (
	maxBuffSizePerReq = 1 << 20 // 1MB max buff size per request
)

var (
	projectID             = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName            = flag.String("bucket", "g-upspin-directory", "The name of an existing bucket within the project.")
	port                  = flag.Int("port", 8081, "TCP port to serve.")
	noAuth                = flag.Bool("noauth", false, "Disable authentication.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
	errEntryNotFound      = dirEntryError{"pathname not found"}
)

type dirServer struct {
	cloudClient gcp.GCP
	httpClient  netutil.HTTPClientInterface
}

type dirEntryError struct {
	error string
}

func (d dirEntryError) Error() string {
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
	// Checks the metadata
	return parsedPath, verifyMetadata(dirEntry.Metadata)
}

// verifyMetadata checks that the metadata portion of the DirEntry is
// minimally valid.
func verifyMetadata(meta upspin.Metadata) error {
	if meta.Sequence < 0 {
		return dirEntryError{"invalid sequence number"}
	}
	return nil
}

// putHandler handles file put requests, for storing or updating
// metadata information.
func (d *dirServer) putHandler(sess *auth.Session, w http.ResponseWriter, r *http.Request) {
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
	err = d.createDirEntry(dirEntry)
	if err != nil {
		netutil.SendJSONError(w, "", err)
		return
	}
	netutil.SendJSONErrorString(w, "success")
}

// createDirEntry will attempt to write a new dirEntry to the back
// end, provided several checks have passed first.
func (d *dirServer) createDirEntry(dirEntry *upspin.DirEntry) error {
	parsedPath, err := verifyDirEntry(dirEntry)
	if err != nil {
		return dirEntryError{fmt.Sprintf("dir entry verification failed: %v", err)}
	}
	// All checks passed so far. Now go put the object into GCE.
	fmt.Printf("Got valid dir entry for path: %v\n", parsedPath)

	err = d.verifyParentWritable(parsedPath)
	if err != nil {
		return dirEntryError{"path is not writable"}
	}

	// Before we can create this entry, we verify that we're not
	// trying to overwrite a file with a directory or a directory
	// with a file. That's probably not what the user wanted
	// anyway.
	path := parsedPath.Path()
	otherDir, err := d.getMeta(path)
	if err != errEntryNotFound {
		if otherDir.Metadata.IsDir && !dirEntry.Metadata.IsDir {
			return dirEntryError{"Overwriting dir with file"}
		}
		if !otherDir.Metadata.IsDir && dirEntry.Metadata.IsDir {
			return dirEntryError{"Overwriting file with dir"}
		}
	}

	// Writes the entry
	return d.putMeta(path, dirEntry)
}

// verifyParentWritable returns an error if the parent dir of a path cannot be written to.
func (d *dirServer) verifyParentWritable(path path.Parsed) error {
	l := len(path.Elems)
	if l <= 1 {
		// The root is a writable directory (modulo ACLs).
		return nil
	}
	// Check that the last entry before the one we're trying to
	// create is already a directory.
	dirEntry, err := d.getMeta(path.Drop(1).Path())
	if err != nil {
		return err
	}
	if !dirEntry.Metadata.IsDir {
		return dirEntryError{"parent directory given is not a directory"}
	}
	return nil
}

// getMeta returns the metadata for the given path.
func (d *dirServer) getMeta(path upspin.PathName) (*upspin.DirEntry, error) {
	log.Printf("Looking up dir entry %q on storage backend\n", path)
	var dirEntry upspin.DirEntry
	buf, err := d.getCloudBytes(path)
	if err != nil {
		return &dirEntry, err
	}
	err = json.Unmarshal(buf, &dirEntry)
	if err != nil {
		return &dirEntry, dirEntryError{fmt.Sprintf("json unmarshal failed retrieving metadata: %v", err)}
	}
	return &dirEntry, nil
}

// putMeta forcibly writes the given dirEntry to the path on the
// backend without checking anything.
func (d *dirServer) putMeta(path upspin.PathName, dirEntry *upspin.DirEntry) error {
	jsonBuf, err := json.Marshal(dirEntry)
	if err != nil {
		return dirEntryError{fmt.Sprintf("conversion to json failed: %v", err)}
	}
	_, err = d.cloudClient.Put(string(path), jsonBuf)
	return err
}

// getCloudBytes fetches the path from the storage backend.
func (d *dirServer) getCloudBytes(path upspin.PathName) ([]byte, error) {
	link, err := d.cloudClient.Get(string(path))
	if err != nil {
		return nil, errEntryNotFound
	}
	// Now use the link to retrieve the metadata.
	req, err := http.NewRequest(netutil.Get, link, nil)
	if err != nil {
		return nil, dirEntryError{err.Error()}
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, dirEntryError{err.Error()}
	}
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, dirEntryError{fmt.Sprintf("error reading cloud: %v", err)}
	}
	return buf, nil
}

func (d *dirServer) getHandler(sess *auth.Session, w http.ResponseWriter, r *http.Request) {
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
	dirEntry, err := d.getMeta(upspin.PathName(pathName))
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// We have a dirEntry. Marshal it and send it back.
	// TODO: verify ACLs before replying.
	log.Printf("Got dir entry for %v: %v", pathName, dirEntry)
	netutil.SendJSONReply(w, dirEntry)
}

func (d *dirServer) listHandler(sess *auth.Session, w http.ResponseWriter, r *http.Request) {
	context := "list: "
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	prefix := r.FormValue("prefix")
	if prefix == "" {
		netutil.SendJSONErrorString(w, "missing prefix in request")
		return
	}
	_, err = path.Parse(upspin.PathName(prefix))
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	names, _, err := d.cloudClient.List(prefix)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	netutil.SendJSONReply(w, &struct{ Names []string }{Names: names})
}

func newDirServer(cloudClient gcp.GCP, httpClient netutil.HTTPClientInterface) *dirServer {
	d := &dirServer{
		cloudClient: cloudClient,
		httpClient:  httpClient,
	}
	return d
}

func main() {
	flag.Parse()

	ah := auth.NewHandler(&auth.Config{
		Lookup: serverauth.PublicUserLookupService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	d := newDirServer(gcp.New(*projectID, *bucketName, gcp.ProjectPrivate), &http.Client{})
	http.HandleFunc("/put", ah.Handle(d.putHandler))
	http.HandleFunc("/get", ah.Handle(d.getHandler))
	http.HandleFunc("/list", ah.Handle(d.listHandler))

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
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
