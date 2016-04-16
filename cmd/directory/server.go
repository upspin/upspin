package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	goPath "path"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cache"
	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
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

	logErr = log.New(os.Stderr, "", log.Ldate|log.Ltime|log.LUTC)
	logMsg = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.LUTC)
)

type dirServer struct {
	cloudClient gcp.GCP // handle for GCP bucket g-upspin-directory
	storeClient *storeClient
	dirCache    *cache.LRU // caches <upspin.PathName, *upspin.DirEntry>. It is thread safe.
	rootCache   *cache.LRU // caches <upspin.UserName, *root>. It is thread safe.
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

// dirHandler handles directory requests. Currently it supports POST and DELETE which implement Put and Delete respectively.
// TODO: support GET also, which implements Lookup.
func (d *dirServer) dirHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case netutil.Delete:
		d.deleteHandler(sess, w, r)
		return
	case netutil.Post:
		// Fall through
	default:
		netutil.SendJSONErrorString(w, "Only POST and DELETE requests are accepted")
		return
	}
	// Handle Put.
	// TODO: move this all into d.putDir.
	const op = "Put"
	buf := netutil.BufferRequest(w, r, maxBuffSizePerReq) // closes r.Body
	if buf == nil {
		// Request was invalid and was closed. Nothing else to do.
		return
	}
	dirEntry := new(upspin.DirEntry)
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
	if err := verifyMetadata(parsed.Path(), dirEntry.Metadata); err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// If we're creating the root, handle it elsewhere.
	if parsed.IsRoot() {
		// We handle root elsewhere because otherwise this code would be riddled with "if IsRoot..."
		d.handleRootCreation(sess, w, &parsed, dirEntry)
		return
	}
	err = d.putDir(sess, &parsed, dirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	logMsg.Printf("%s: %q %q", op, sess.User(), dirEntry.Name)
	netutil.SendJSONErrorString(w, "success")
}

// putDir writes or overwrites a complete dirEntry to the back
// end, provided several checks have passed first.
func (d *dirServer) putDir(sess auth.Session, parsed *path.Parsed, dirEntry *upspin.DirEntry) error {
	const op = "Put"

	// Check ACLs before we go any further, so we don't leak information about the existence of files and directories.
	canCreate, err := d.hasRight(op, sess.User(), access.Create, dirEntry.Name)
	if err != nil {
		return newDirError(op, dirEntry.Name, err.Error())
	}
	canWrite, err := d.hasRight(op, sess.User(), access.Write, dirEntry.Name)
	if err != nil {
		return newDirError(op, dirEntry.Name, err.Error())
	}
	if dirEntry.Metadata.IsDir && !canCreate || !dirEntry.Metadata.IsDir && !canWrite {
		return newDirError(op, dirEntry.Name, access.ErrPermissionDenied.Error())
	}
	// Find parent.
	parentParsedPath := parsed.Drop(1) // Can't fail as this is not called for roots.
	parentDirEntry, err := d.getDirEntry(&parentParsedPath)
	if err != nil {
		if err == errEntryNotFound {
			// Give a more descriptive error
			err = newDirError(op, parsed.Path(), "parent path not found")
		}
		return err
	}
	// Verify parent IsDir (redundant, but just to be safe).
	if !parentDirEntry.Metadata.IsDir {
		logErr.Printf("WARN: bad inconsistency. Parent of path is not a directory: %s", parentDirEntry.Name)
		return newDirError(op, parsed.Path(), "parent is not a directory")
	}

	// Verify whether there's a directory with same name.
	canonicalPath := parsed.Path()
	existingDirEntry, err := d.getNonRoot(canonicalPath)
	if err != nil && err != errEntryNotFound {
		return newDirError(op, canonicalPath, err.Error())

	}
	if err == nil {
		if existingDirEntry.Metadata.IsDir {
			return newDirError(op, canonicalPath, "directory already exists")
		}
		if dirEntry.Metadata.IsDir {
			return newDirError(op, canonicalPath, "overwriting file with directory")
		}
	}

	// Canonicalize path.
	dirEntry.Name = canonicalPath

	// Finally, store the new entry.
	err = d.putNonRoot(canonicalPath, dirEntry)
	if err != nil {
		return err
	}

	// Patch the parent: bump sequence number.
	parentDirEntry.Metadata.Sequence++
	err = d.putDirEntry(&parentParsedPath, parentDirEntry)
	if err != nil {
		return err
	}

	// If this is an Access file or Group file, we have some extra work to do.
	if access.IsAccessFile(canonicalPath) {
		err = d.updateAccess(parsed, &dirEntry.Location)
		if err != nil {
			return err
		}
	}
	if access.IsGroupFile(canonicalPath) {
		logMsg.Printf("Invalidating group file %s", canonicalPath)
		// By removing the group we guarantee we won't be using its old definition, if any.
		// Since we parse groups lazily, this is correct and generally efficient.
		_ = access.RemoveGroup(canonicalPath) // error is ignored on purpose. If group was not there, no harm done.
	}

	return nil
}

func (d *dirServer) getHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	const op = "Get"
	pathnames := d.verifyFormParams(op, "", w, r, "pathname")
	if len(pathnames) == 0 {
		// Nothing to be done. Error sent to client.
		return
	}
	parsedPath, err := path.Parse(upspin.PathName(pathnames[0]))
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Check ACLs before attempting to read the dirEntry to avoid leaking information about the existence of paths.
	canRead, err := d.hasRight(op, sess.User(), access.Read, parsedPath.Path())
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		logErr.Printf("Access error Read: %s", err)
		netutil.SendJSONError(w, context, err)
		return
	}
	canList, err := d.hasRight(op, sess.User(), access.List, parsedPath.Path())
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		logErr.Printf("Access error List: %s", err)
		netutil.SendJSONError(w, context, err)
		return
	}
	// If the user has no rights, we're done.
	if !canRead && !canList {
		netutil.SendJSONError(w, context, newDirError(op, parsedPath.Path(), access.ErrPermissionDenied.Error()))
		return
	}
	// Look up entry
	var dirEntry *upspin.DirEntry
	if !parsedPath.IsRoot() {
		dirEntry, err = d.getNonRoot(parsedPath.Path())
	} else {
		root, err := d.getRoot(parsedPath.User)
		if err == nil {
			dirEntry = root.dirEntry
		}
	}
	if err != nil {
		if err == errEntryNotFound {
			err = newDirError("get", parsedPath.Path(), "path not found")
		}
		netutil.SendJSONError(w, context, err)
		return
	}
	// We have a dirEntry and ACLs check. But we still must clear Location if user does not have Read rights.
	if !canRead {
		logMsg.Printf("Zeroing out location information in Get for user %s on path %s", sess.User(), parsedPath)
		dirEntry.Location = upspin.Location{}
	}
	logMsg.Printf("Got dir entry for user %s: path %s: %s", sess.User(), parsedPath.Path(), dirEntry)
	netutil.SendJSONReply(w, dirEntry)
}

func (d *dirServer) globHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	const op = "Glob"
	patterns := d.verifyFormParams(op, "", w, r, "pattern")
	if len(patterns) == 0 {
		// Nothing to be done. Error sent to client.
		return
	}
	pathPattern := upspin.PathName(patterns[0])
	parsed, err := path.Parse(pathPattern)
	if err != nil {
		netutil.SendJSONError(w, context, newDirError(op, pathPattern, err.Error()))
		return
	}
	// Check if pattern is a valid go path pattern
	_, err = goPath.Match(parsed.FilePath(), "")
	if err != nil {
		netutil.SendJSONError(w, context, newDirError(op, pathPattern, err.Error()))
		return
	}

	// As an optimization, we look for the longest prefix that
	// does not contain a metacharacter -- this saves us from
	// doing a full list operation if the matter of interest is
	// deep in a sub directory.
	clear := len(parsed.Elems)
	for i, elem := range parsed.Elems {
		if strings.ContainsAny(elem, "*?[]^") {
			clear = i
			break
		}
	}
	prefix := parsed.First(clear).String()
	depth := len(parsed.Elems) - clear

	var names []string
	if depth == 1 {
		if !strings.HasSuffix(prefix, "/") {
			prefix = prefix + "/"
		}
		names, err = d.cloudClient.ListDir(prefix)
	} else {
		names, err = d.cloudClient.ListPrefix(prefix, int(depth))
	}
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	dirEntries := make([]*upspin.DirEntry, 0, len(names))
	// Now do the actual globbing.
	for _, path := range names {
		// error is ignored as pattern is known valid
		if match, _ := goPath.Match(patterns[0], path); match {
			// Now fetch each DirEntry we need
			logMsg.Printf("Looking up: %s for glob %s", path, patterns[0])
			de, err := d.getNonRoot(upspin.PathName(path))
			if err != nil {
				netutil.SendJSONError(w, context, newDirError(op, pathPattern, err.Error()))
			}
			// Verify if user has proper list ACL.
			canList, err := d.hasRight(op, sess.User(), access.List, de.Name)
			if err != nil {
				logErr.Printf("Error checking access for user: %s on %s: %s", sess.User(), de.Name, err)
				continue
			}
			canRead, err := d.hasRight(op, sess.User(), access.Read, de.Name)
			if err != nil {
				logErr.Printf("Error checking access for user: %s on %s: %s", sess.User(), de.Name, err)
				continue
			}
			if !canRead && !canList {
				logMsg.Printf("User %s can't Glob %s", sess.User(), de.Name)
				continue
			}
			// If the user can't read a path, clear out its Location.
			if !canRead {
				de.Location = upspin.Location{}
			}
			dirEntries = append(dirEntries, de)
		}
	}
	netutil.SendJSONReply(w, dirEntries)
}

// verifyFormParams parses the request form and looks for the presence of each one of the listed fields.
// If a field is not found, it returns an error to the user. If all are found, it returns their value in
// the same order as requested.
func (d *dirServer) verifyFormParams(op string, path upspin.PathName, w http.ResponseWriter, r *http.Request, fields ...string) []string {
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return nil
	}
	values := make([]string, len(fields))
	for i, k := range fields {
		v := r.FormValue(k)
		if v == "" {
			errMsg := fmt.Sprintf("missing %s in request", k)
			logErr.Print(errMsg)
			netutil.SendJSONError(w, context, newDirError(op, path, errMsg))
			return nil
		}
		values[i] = v
	}
	return values
}

// deleteHandler handles deleting names.
// TODO: This will soon become a simple helper function to support dirHandler.
func (d *dirServer) deleteHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	const op = "Delete"
	pathname := r.URL.Path[5:] // 5 => skip "/dir/"
	logMsg.Printf("User %s attempting to delete %s", sess.User(), pathname)
	parsed, err := path.Parse(upspin.PathName(pathname))
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	parsedPath := parsed.Path()
	// Check ACLs before attempting to get the dirEntry to avoid leaking information about the existence of paths.
	canDelete, err := d.hasRight(op, sess.User(), access.Delete, parsedPath)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		logErr.Printf("Access error Delete: %s", err)
		netutil.SendJSONError(w, context, err)
		return
	}
	if !canDelete {
		err = newDirError(op, parsedPath, access.ErrPermissionDenied.Error())
		netutil.SendJSONError(w, context, err)
		return
	}
	// Otherwise, locate the entry first.
	dirEntry, err := d.getDirEntry(&parsed)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Only empty directories can be removed.
	if dirEntry.Metadata.IsDir {
		err = d.isDirEmpty(parsedPath)
		if err != nil {
			netutil.SendJSONError(w, context, newDirError(op, parsedPath, err.Error()))
			return
		}
	}
	// Attempt to delete it from GCP.
	if err = d.deleteCloudEntry(parsedPath); err != nil {
		err = newDirError(op, parsedPath, err.Error())
		netutil.SendJSONError(w, context, err)
		return
	}
	netutil.SendJSONErrorString(w, "success")
}

func newDirServer(cloudClient gcp.GCP, store *storeClient) *dirServer {
	d := &dirServer{
		cloudClient: cloudClient,
		storeClient: store,
		dirCache:    cache.NewLRU(1000), // TODO: adjust numbers
		rootCache:   cache.NewLRU(1000), // TODO: adjust numbers
	}
	return d
}

func main() {
	flag.Parse()

	ah := auth.NewHandler(&auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	s := newStoreClient(auth.NewClient(dirServerName, auth.NewFactotum(&upspin.Context{KeyPair: dirServerKeys}), &http.Client{}))
	d := newDirServer(gcp.New(*projectID, *bucketName, gcp.ProjectPrivate), s)

	// TODO: put and get are HTTP verbs so this is ambiguous. Change this here
	// and in clients to /dir and /lookup respectively.
	http.HandleFunc("/put", ah.Handle(d.dirHandler))
	http.HandleFunc("/dir", ah.Handle(d.dirHandler)) // First step in resolving the TODO above.
	http.HandleFunc("/get", ah.Handle(d.getHandler))
	http.HandleFunc("/glob", ah.Handle(d.globHandler))

	if *sslCertificateFile != "" && *sslCertificateKeyFile != "" {
		server, err := auth.NewSecureServer(*port, *sslCertificateFile, *sslCertificateKeyFile)
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
