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

// dirHandler handles file put requests, for storing or updating
// metadata information.
func (d *dirServer) dirHandler(sess auth.Session, w http.ResponseWriter, r *http.Request) {
	const op = "Put"
	if r.Method != netutil.Post && r.Method != netutil.Patch {
		netutil.SendJSONErrorString(w, "/put only handles POST or PATCH HTTP requests")
		return
	}
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

	// TODO: verify ACLs before applying dir entry

	switch r.Method {
	case netutil.Post:
		d.putDirHandler(sess, w, &parsed, dirEntry)
	case netutil.Patch:
		d.patchHandler(sess, w, &parsed, dirEntry)
	default:
		netutil.SendJSONError(w, context, fmt.Errorf("invalid HTTP method: %q", r.Method))
	}
}

// patchHandler handles directory patch requests, for making partial updates to directory entries. parsedPath is a validated dirEntry.Name.
func (d *dirServer) patchHandler(sess auth.Session, w http.ResponseWriter, parsedPath *path.Parsed, dirEntry *upspin.DirEntry) {
	const op = "patch"
	// Check that only allowed fields are being updated.
	dirEntry.Name = "" // Name is not updatable.
	if err := d.verifyUpdatableFields(dirEntry); err != nil {
		netutil.SendJSONError(w, context, newDirError(op, parsedPath.Path(), err.Error()))
		return
	}
	// Lookup original dir entry.
	origDirEntry, err := d.getDirEntry(parsedPath)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Merge fields.
	mergedDirEntry := d.mergeDirEntries(origDirEntry, dirEntry) // NOTE: mergedDirEntry is an alias for origDirEntry.
	// Apply mutation on stable storage.
	err = d.putDirEntry(parsedPath, mergedDirEntry)
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
	if src.Metadata.PackData != nil {
		dst.Metadata.PackData = src.Metadata.PackData
	}
	return dst
}

// putDirHandler writes or overwrites a complete dirEntry to the back
// end, provided several checks have passed first.
func (d *dirServer) putDirHandler(sess auth.Session, w http.ResponseWriter, parsed *path.Parsed, dirEntry *upspin.DirEntry) {
	const op = "Put"
	if err := verifyMetadata(parsed.Path(), dirEntry.Metadata); err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Get the parent dir, unless we're creating the root.
	if parsed.IsRoot() {
		// We handle root elsewhere because otherwise this code would be riddled with "if IsRoot..."
		d.handleRootCreation(sess, w, parsed, dirEntry)
		return
	}
	parentParsedPath := parsed.Drop(1)
	parentDirEntry, err := d.getDirEntry(&parentParsedPath)
	if err != nil {
		if err == errEntryNotFound {
			// Give a more descriptive error
			err = newDirError(op, parsed.Path(), "parent path not found")
		}
		netutil.SendJSONError(w, context, err)
		return
	}
	// Verify parent IsDir (redundant, but just to be safe).
	if !parentDirEntry.Metadata.IsDir {
		logErr.Printf("WARN: bad inconsistency. Parent of path is not a directory: %s", parentDirEntry.Name)
		netutil.SendJSONError(w, context, newDirError(op, parsed.Path(), "parent is not a directory"))
		return
	}

	// Verify whether there's a directory with same name.
	canonicalPath := parsed.Path()
	existingDirEntry, err := d.getNonRoot(canonicalPath)
	if err != nil && err != errEntryNotFound {
		netutil.SendJSONError(w, context, newDirError(op, canonicalPath, err.Error()))
		return
	}
	if err == nil {
		if existingDirEntry.Metadata.IsDir {
			netutil.SendJSONError(w, context, newDirError(op, canonicalPath, "directory already exists"))
			return
		}
		if dirEntry.Metadata.IsDir {
			netutil.SendJSONError(w, context, newDirError(op, canonicalPath, "overwriting file with directory"))
			return
		}
	}

	// Canonicalize path.
	dirEntry.Name = canonicalPath

	// Finally, store the new entry.
	err = d.putNonRoot(canonicalPath, dirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	// Patch the parent: bump sequence number.
	parentDirEntry.Metadata.Sequence++
	err = d.putDirEntry(&parentParsedPath, parentDirEntry)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	// If this is an Access file or Group file, we have some extra work to do.
	if access.IsAccessFile(canonicalPath) {
		err = d.updateAccess(parsed, &dirEntry.Location)
		if err != nil {
			netutil.SendJSONError(w, context, err)
			return
		}
	}
	// TODO: if IsGroup(canonicalPath) must potentially re-parse all Access files.

	logMsg.Printf("%s: %q %q", op, sess.User(), dirEntry.Name)
	netutil.SendJSONErrorString(w, "success")
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
	// Check ACLs
	canRead, err := d.hasRight(op, sess.User(), access.Read, dirEntry.Name)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		logErr.Printf("Access error: %s", err)
		netutil.SendJSONError(w, context, err)
		return
	}
	log.Printf("=== Can read: %s: %v", dirEntry.Name, canRead)
	if !canRead {
		err = newDirError(op, dirEntry.Name, fmt.Sprintf("permission denied: user %s cannot %s", sess.User(), access.Read))
		logErr.Print(err)
		netutil.SendJSONError(w, context, err)
		return
	}
	// We have a dirEntry and ACLs check. Marshal it and send it back.
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
			hasAccess, err := d.hasRight(op, sess.User(), access.List, de.Name)
			if err != nil {
				logErr.Printf("Error checking access for user: %s on %s: %s", sess.User(), de.Name, err)
				continue
			}
			if hasAccess {
				dirEntries = append(dirEntries, de)
			}
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
