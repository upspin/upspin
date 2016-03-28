package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cmd/serverauth"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

const (
	maxBuffSizePerReq = 1 << 20 // 1MB max buff size per request
	context           = "DirService: "
)

var (
	projectID             = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName            = flag.String("bucket", "g-upspin-directory", "The name of an existing bucket within the project.")
	port                  = flag.Int("port", 8081, "TCP port to serve.")
	noAuth                = flag.Bool("noauth", false, "Disable authentication.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
	errEntryNotFound      = newDirError("download", "", "pathname not found")

	logErr = log.New(os.Stderr, "", log.Ldate|log.Ltime|log.LUTC)
	logMsg = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.LUTC)
)

type dirServer struct {
	cloudClient gcp.GCP // handle for GCP bucket g-upspin-directory
}

type dirError struct {
	op    string
	path  upspin.PathName
	error string
}

func (d dirError) Error() string {
	var buf bytes.Buffer
	if d.op != "" {
		buf.WriteString(d.op)
		buf.WriteString(": ")
	}
	if len(d.path) > 0 {
		buf.WriteString(string(d.path))
		buf.WriteString(": ")
	}
	buf.WriteString(d.error)
	return buf.String()
}

func newDirError(op string, path upspin.PathName, err string) *dirError {
	return &dirError{
		op:    op,
		path:  path,
		error: err,
	}
}

// verifyMetadata checks that the metadata is minimally valid.
func verifyMetadata(path upspin.PathName, meta upspin.Metadata) error {
	if meta.Sequence < 0 {
		return newDirError("verifyMeta", path, "invalid sequence number")
	}
	return nil
}

// putHandler handles file put requests, for storing or updating
// metadata information.
func (d *dirServer) putHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	const op = "Put"
	if r.Method != netutil.Post && r.Method != netutil.Put && r.Method != netutil.Patch {
		netutil.SendJSONErrorString(w, "/put only handles POST, PUT or PATCH HTTP requests")
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
		retErr := newDirError(op, dirEntry.Name, fmt.Sprintf("unmarshal: %s", err))
		netutil.SendJSONError(w, context, retErr)
		logErr.Println(retErr)
		return
	}
	parsed, err := path.Parse(dirEntry.Name) // canonicalizes dirEntry.Name
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	// TODO: verify ACLs before applying put.

	// If this is a partial update, handle it completely as such.
	if r.Method == netutil.Patch {
		d.patchHandler(sess, w, parsed.Path(), dirEntry)
		return
	}
	err = d.createDirEntry(op, &parsed, dirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		logErr.Println(err)
		return
	}
	logMsg.Printf("%s: %q %q", op, sess.User(), dirEntry.Name)
	netutil.SendJSONErrorString(w, "success")
}

// patchHandler handles directory patch requests, for making partial updates to directory entries. parsedPath is a validated dirEntry.Name.
func (d *dirServer) patchHandler(sess auth.Session, w http.ResponseWriter, parsedPath upspin.PathName, dirEntry *upspin.DirEntry) {
	const op = "patch"
	// Check that only allowed fields are being updated.
	dirEntry.Name = "" // Name is not updatable.
	if err := d.verifyUpdatableFields(dirEntry); err != nil {
		netutil.SendJSONError(w, context, newDirError(op, parsedPath, err.Error()))
		return
	}
	// Lookup original dir entry.
	origDirEntry, err := d.getMeta(parsedPath)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Merge fields.
	mergedDirEntry := d.mergeDirEntries(origDirEntry, dirEntry) // NOTE: mergedDirEntry is an alias for origDirEntry.
	// Apply mutation on stable storage.
	err = d.putMeta(parsedPath, mergedDirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	logMsg.Printf("%s: %q %q", op, sess.User(), mergedDirEntry.Name)
	netutil.SendJSONErrorString(w, "success")
}

// verifyNonUpdatableFields reports an error if a partial dirEntry contains non-updatable fields.
func (d *dirServer) verifyUpdatableFields(dir *upspin.DirEntry) error {
	if dir.Name != "" {
		return errors.New("Name is not updatable")
	}
	// Location may be updatable in the future, but right now it is not supported.
	var zeroLoc upspin.Location
	if dir.Location != zeroLoc {
		return errors.New("Location is not updatable")
	}
	// Here we're simply checking whether there is a non-zero value in IsDir.
	if dir.Metadata.IsDir {
		return errors.New("IsDir is not updatable")
	}
	// All other metadata fields are updatable.
	return nil
}

// mergeDirEntries merges dst and src together and returns dst. Only updatable fields are merged.
func (d *dirServer) mergeDirEntries(dst, src *upspin.DirEntry) *upspin.DirEntry {
	if src.Metadata.Sequence != 0 {
		dst.Metadata.Sequence = src.Metadata.Sequence
	}
	if src.Metadata.Size != 0 {
		dst.Metadata.Size = src.Metadata.Size
	}
	if src.Metadata.Time != 0 {
		dst.Metadata.Time = src.Metadata.Time
	}
	if src.Metadata.Readers != nil {
		dst.Metadata.Readers = src.Metadata.Readers
	}
	if src.Metadata.PackData != nil {
		dst.Metadata.PackData = src.Metadata.PackData
	}
	return dst
}

// createDirEntry will attempt to write a new dirEntry to the back
// end, provided several checks have passed first.
func (d *dirServer) createDirEntry(op string, parsed *path.Parsed, dirEntry *upspin.DirEntry) error {
	err := verifyMetadata(parsed.Path(), dirEntry.Metadata)
	if err != nil {
		return err
	}
	err = d.verifyParentWritable(parsed)
	if err != nil {
		return err
	}

	// Before we can create this entry, we verify that we're not
	// trying to overwrite a file with a directory or a directory
	// with a file. That's probably not what the user wanted
	// anyway.
	path := parsed.Path()
	otherDir, err := d.getMeta(path)
	if err != nil && err != errEntryNotFound {
		return newDirError(op, path, err.Error())
	}
	if err == nil {
		if otherDir.Metadata.IsDir {
			return newDirError(op, path, "directory already exists")
		}
		if dirEntry.Metadata.IsDir {
			return newDirError(op, path, "overwriting file with directory")
		}
	}
	// Either err is nil (dir entry existed and is not ovewriting the wrong kind of entry) or it's not found.
	// In both cases, we proceed to creating or overwriting the entry.

	if !parsed.IsRoot() {
		// Canonicalize the pathname if not root
		dirEntry.Name = path
	} else {
		// The root is special. Remove the backslash so that GCP has a file with metadata for the root.
		path = upspin.PathName(parsed.User)
	}

	// Writes the entry
	return d.putMeta(path, dirEntry)
}

// verifyParentWritable returns an error if the parent dir of a path cannot be written to.
func (d *dirServer) verifyParentWritable(path *path.Parsed) error {
	if path.IsRoot() {
		// The root is a writable directory (modulo ACLs).
		return nil
	}
	// Check that the last entry before the one we're trying to
	// create is already a directory.
	dirEntry, err := d.getMeta(path.Drop(1).Path())
	if err != nil {
		if err == errEntryNotFound {
			return newDirError("verify", path.Path(), "parent path not found")
		}
		return newDirError("verify", path.Path(), err.Error())
	}
	if !dirEntry.Metadata.IsDir {
		return newDirError("verify", path.Path(), "parent of path is not a directory")
	}
	return nil
}

// getMeta returns the metadata for the given path.
func (d *dirServer) getMeta(path upspin.PathName) (*upspin.DirEntry, error) {
	logMsg.Printf("Looking up dir entry %q on storage backend", path)
	var dirEntry upspin.DirEntry
	buf, err := d.getCloudBytes(path)
	if err != nil {
		return &dirEntry, err
	}
	err = json.Unmarshal(buf, &dirEntry)
	if err != nil {
		return &dirEntry, newDirError("getmeta", path, fmt.Sprintf("json unmarshal failed retrieving metadata: %v", err))
	}
	return &dirEntry, nil
}

// putMeta forcibly writes the given dirEntry to the canonical path on the
// backend without checking anything.
func (d *dirServer) putMeta(path upspin.PathName, dirEntry *upspin.DirEntry) error {
	// TODO(ehg)  if using crypto packing here, as we should, how will secrets get to code at service startup?
	jsonBuf, err := json.Marshal(dirEntry)
	if err != nil {
		// This is really bad. It means we created a DirEntry that does not marshal to JSON.
		errMsg := fmt.Sprintf("internal server error: conversion to json failed: %s", err)
		logErr.Printf("WARN: %s: %s: %+v", errMsg, path, dirEntry)
		return newDirError("putmeta", path, errMsg)
	}
	logMsg.Printf("Storing dir entry at %q", path)
	_, err = d.cloudClient.Put(string(path), jsonBuf)
	return err
}

// getCloudBytes fetches the path from the storage backend.
func (d *dirServer) getCloudBytes(path upspin.PathName) ([]byte, error) {
	data, err := d.cloudClient.Download(string(path))
	if err != nil {
		return nil, errEntryNotFound
	}
	return data, err
}

func (d *dirServer) getHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	pathName := r.FormValue("pathname")
	if pathName == "" {
		netutil.SendJSONErrorString(w, context+"missing pathname in request")
		return
	}
	path := upspin.PathName(pathName)
	dirEntry, err := d.getMeta(path)
	if err != nil {
		if err == errEntryNotFound {
			err = newDirError("get", path, "path not found")
		}
		netutil.SendJSONError(w, context, err)
		return
	}
	// We have a dirEntry. Marshal it and send it back.
	// TODO: verify ACLs before replying.
	logMsg.Printf("Got dir entry for user %s: path %s: %s", sess.User(), pathName, dirEntry)
	netutil.SendJSONReply(w, dirEntry)
}

func (d *dirServer) listHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	prefix := r.FormValue("prefix")
	if prefix == "" {
		netutil.SendJSONErrorString(w, context+"missing prefix in request")
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
	logMsg.Printf("List request for prefix %q", prefix)
	netutil.SendJSONReply(w, &struct{ Names []string }{Names: names})
}

func newDirServer(cloudClient gcp.GCP) *dirServer {
	d := &dirServer{
		cloudClient: cloudClient,
	}
	return d
}

func main() {
	flag.Parse()

	ah := auth.NewHandler(&auth.Config{
		Lookup: serverauth.PublicUserLookupService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	d := newDirServer(gcp.New(*projectID, *bucketName, gcp.ProjectPrivate))
	http.HandleFunc("/put", ah.Handle(d.putHandler))
	http.HandleFunc("/get", ah.Handle(d.getHandler))
	http.HandleFunc("/list", ah.Handle(d.listHandler))

	if *sslCertificateFile != "" && *sslCertificateKeyFile != "" {
		server, err := serverauth.NewSecureServer(*port, *sslCertificateFile, *sslCertificateKeyFile)
		if err != nil {
			logErr.Fatal(err)
		}
		logErr.Println("Starting HTTPS server with SSL.")
		logErr.Fatal(server.ListenAndServeTLS(*sslCertificateFile, *sslCertificateKeyFile))
	} else {
		logErr.Println("Not using SSL certificate. Starting regular HTTP server.")
		logErr.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
	}
	logErr.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
