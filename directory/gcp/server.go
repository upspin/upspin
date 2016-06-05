// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"bytes"
	"errors"
	"fmt"
	goPath "path"
	"strings"
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/cache"
	gcpCloud "upspin.io/cloud/gcp"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/store/remote"
	_ "upspin.io/user/remote"
)

var (
	dirServerName = upspin.UserName("upspin-dir@upspin.io")
)

// Configuration options for this package.
const (
	// ConfigProjectID specifies which GCP project to use for talking to GCP.
	// If not specified, "upspin" is used.
	ConfigProjectID = "gcpProjectId"

	// ConfigBucketName specifies which GCS bucket to store data in.
	// If not specified, "g-upspin-store" is used.
	ConfigBucketName = "gcpBucketName"
)

type dirServer struct {
	context        upspin.Context
	endpoint       upspin.Endpoint
	timeNow        func() upspin.Time // returns the current time
	cloudClient    gcpCloud.GCP       // handle for GCP bucket g-upspin-directory
	factotum       upspin.Factotum    // this server's factotum with its keys.
	newStoreClient newStoreClient     // how to create a Store client.
	dirCache       *cache.LRU         // caches <upspin.PathName, upspin.DirEntry>. It is thread safe.
	rootCache      *cache.LRU         // caches <upspin.UserName, root>. It is thread safe.
	dirNegCache    *cache.LRU         // caches the absence of a path <upspin.PathName, nil>. It is thread safe.
}

var _ upspin.Directory = (*dirServer)(nil)

var zeroLoc upspin.Location

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

// MakeDirectory implements upspin.Directory.
func (d *dirServer) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	const op = "MakeDirectory"

	parsed, err := path.Parse(dirName)
	if err != nil {
		return zeroLoc, newDirError(op, dirName, err.Error())
	}
	// Prepares a dir entry for storage.
	dirEntry := upspin.DirEntry{
		Name: parsed.Path(),
		Location: upspin.Location{
			// Reference is ignored.
			// Endpoint for dir entries is where the Directory server is.
			Endpoint: d.endpoint,
		},
		Metadata: upspin.Metadata{
			Attr:     upspin.AttrDirectory,
			Sequence: 0, // don't care?
			Size:     0, // Being explicit that dir entries have zero size.
			Time:     d.timeNow(),
			Packdata: nil,
		},
	}
	err = d.put(op, &dirEntry)
	if err != nil {
		return zeroLoc, err
	}
	return dirEntry.Location, nil
}

// Put writes or overwrites a complete dirEntry to the back end, provided several checks have passed first.
// It implements upspin.Directory.
func (d *dirServer) Put(dirEntry *upspin.DirEntry) error {
	const op = "Put"
	return d.put(op, dirEntry)
}

// put is the common implementation between MakeDirectory and Put.
func (d *dirServer) put(op string, dirEntry *upspin.DirEntry) error {
	parsed, err := path.Parse(dirEntry.Name) // canonicalizes dirEntry.Name
	if err != nil {
		return newDirError(op, dirEntry.Name, err.Error())
	}
	user := d.context.UserName
	if err := verifyMetadata(parsed.Path(), dirEntry.Metadata); err != nil {
		return err
	}
	// If we're creating the root, handle it elsewhere.
	if parsed.IsRoot() {
		// We handle root elsewhere because otherwise this code would be riddled with "if IsRoot..."
		return d.handleRootCreation(user, &parsed, dirEntry)
	}

	// Check ACLs before we go any further, so we don't leak information about the existence of files and directories.
	canCreate, err := d.hasRight(op, user, access.Create, &parsed)
	if err != nil {
		return newDirError(op, dirEntry.Name, err.Error())
	}
	canWrite, err := d.hasRight(op, user, access.Write, &parsed)
	if err != nil {
		return newDirError(op, dirEntry.Name, err.Error())
	}
	if dirEntry.IsDir() && !canCreate || !dirEntry.IsDir() && !canWrite {
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
	if !parentDirEntry.IsDir() {
		log.Error.Printf("Bad inconsistency. Parent of path is not a directory: %s", parentDirEntry.Name)
		return newDirError(op, parsed.Path(), "parent is not a directory")
	}

	// Verify whether there's a directory with same name.
	canonicalPath := parsed.Path()
	existingDirEntry, err := d.getNonRoot(canonicalPath)
	if err != nil && err != errEntryNotFound {
		return newDirError(op, canonicalPath, err.Error())

	}
	if err == nil {
		if existingDirEntry.IsDir() {
			return newDirError(op, canonicalPath, "directory already exists")
		}
		if dirEntry.IsDir() {
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
		err = d.updateAccess(&parsed, &dirEntry.Location)
		if err != nil {
			return err
		}
	}
	if access.IsGroupFile(canonicalPath) {
		log.Printf("Invalidating group file %s", canonicalPath)
		// By removing the group we guarantee we won't be using its old definition, if any.
		// Since we parse groups lazily, this is correct and generally efficient.
		_ = access.RemoveGroup(canonicalPath) // error is ignored on purpose. If group was not there, no harm done.
	}

	log.Printf("%s: %q %q", op, user, dirEntry.Name)
	return nil
}

// Lookup implements upspin.Directory.
func (d *dirServer) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "Lookup"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, newDirError(op, pathName, err.Error())
	}

	// Check ACLs before attempting to read the dirEntry to avoid leaking information about the existence of paths.
	canRead, err := d.hasRight(op, d.context.UserName, access.Read, &parsed)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		log.Printf("Access error Read: %s", err)
		return nil, err
	}
	canList, err := d.hasRight(op, d.context.UserName, access.List, &parsed)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		log.Printf("Access error List: %s", err)
		return nil, err
	}
	// If the user has no rights, we're done.
	if !canRead && !canList {
		return nil, newDirError(op, parsed.Path(), access.ErrPermissionDenied.Error())
	}
	// Look up entry
	var dirEntry *upspin.DirEntry
	if !parsed.IsRoot() {
		dirEntry, err = d.getNonRoot(parsed.Path())
	} else {
		root, err := d.getRoot(parsed.User())
		if err == nil {
			dirEntry = &root.dirEntry
		}
	}
	if err != nil {
		if err == errEntryNotFound {
			err = newDirError(op, parsed.Path(), "path not found")
		}
		return nil, err
	}
	// We have a dirEntry and ACLs check. But we still must clear Location if user does not have Read rights.
	if !canRead {
		log.Printf("Zeroing out location information in Get for user %s on path %s", d.context.UserName, parsed)
		dirEntry.Location = upspin.Location{}
		dirEntry.Metadata.Packdata = nil
	}
	log.Printf("Got dir entry for user %s: path %s: %v", d.context.UserName, parsed.Path(), dirEntry)
	return dirEntry, nil
}

func (d *dirServer) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	const op = "WhichAccess"

	parsed, err := path.Parse(pathName)
	if err != nil {
		return "", newDirError(op, pathName, err.Error())
	}

	accessPath, acc, err := d.whichAccess(op, &parsed)
	if err != nil {
		return "", err
	}

	user := d.context.UserName

	// Must check whether the user has sufficient rights to List the requested path.
	canRead, err := d.checkRights(user, access.Read, parsed.Path(), acc)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		log.Printf("WhichAccess error Read: %s", err)
		return "", err
	}
	canList, err := d.checkRights(user, access.List, parsed.Path(), acc)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		log.Printf("WhichAccess error List: %s", err)
		return "", err
	}
	if !canRead && !canList {
		return "", newDirError(op, parsed.Path(), access.ErrPermissionDenied.Error())
	}
	return accessPath, nil
}

func (d *dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "Glob"
	pathName := upspin.PathName(pattern)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, newDirError(op, pathName, err.Error())
	}
	// Check if pattern is a valid go path pattern
	_, err = goPath.Match(parsed.FilePath(), "")
	if err != nil {
		return nil, newDirError(op, parsed.Path(), err.Error())
	}

	// As an optimization, we look for the longest prefix that
	// does not contain a metacharacter -- this saves us from
	// doing a full list operation if the matter of interest is
	// deep in a sub directory.
	clear := parsed.NElem()
	for i := 0; i < clear; i++ {
		if strings.ContainsAny(parsed.Elem(i), "*?[]^") {
			clear = i
			break
		}
	}
	prefix := parsed.First(clear).String()
	depth := parsed.NElem() - clear

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
		return nil, err
	}

	user := d.context.UserName
	dirEntries := make([]*upspin.DirEntry, 0, len(names))
	// Now do the actual globbing.
	for _, lookupPath := range names {
		// error is ignored as pattern is known valid
		if match, _ := goPath.Match(parsed.String(), lookupPath); match {
			// Now fetch each DirEntry we need
			log.Printf("Looking up: %s for glob %s", lookupPath, parsed.String())
			de, err := d.getNonRoot(upspin.PathName(lookupPath))
			if err != nil {
				return nil, newDirError(op, parsed.Path(), err.Error())
			}
			// Verify if user has proper list ACL.
			parsedDirName, err := path.Parse(de.Name)
			if err != nil {
				log.Error.Printf("Internal inconsistency: dir entry name does not parse: %s", err)
				continue
			}
			canList, err := d.hasRight(op, user, access.List, &parsedDirName)
			if err != nil {
				log.Printf("Error checking access for user: %s on %s: %s", user, de.Name, err)
				continue
			}
			canRead, err := d.hasRight(op, user, access.Read, &parsedDirName)
			if err != nil {
				log.Printf("Error checking access for user: %s on %s: %s", user, de.Name, err)
				continue
			}
			if !canRead && !canList {
				log.Printf("User %s can't Glob %s", user, de.Name)
				continue
			}
			// If the user can't read a path, clear out its Location.
			if !canRead {
				de.Location = upspin.Location{}
				de.Metadata.Packdata = nil
			}
			dirEntries = append(dirEntries, de)
		}
	}
	return dirEntries, nil
}

// deleteDirEntry handles deleting names and their associated DirEntry.
func (d *dirServer) Delete(pathName upspin.PathName) error {
	const op = "Delete"

	parsed, err := path.Parse(pathName)
	if err != nil {
		return newDirError(op, pathName, err.Error())
	}

	user := d.context.UserName
	// Check ACLs before attempting to get the dirEntry to avoid leaking information about the existence of paths.
	canDelete, err := d.hasRight(op, user, access.Delete, &parsed)
	if err != nil {
		err = newDirError(op, "", err.Error()) // path is included in the original error message.
		log.Printf("Access error for Delete: %s", err)
		return err
	}
	if !canDelete {
		return newDirError(op, parsed.Path(), access.ErrPermissionDenied.Error())
	}
	// Otherwise, locate the entry first.
	dirEntry, err := d.getDirEntry(&parsed)
	if err != nil {
		return err
	}
	parsedPath := parsed.Path()
	// Only empty directories can be removed.
	if dirEntry.IsDir() {
		err = d.isDirEmpty(parsedPath)
		if err != nil {
			return newDirError(op, parsedPath, err.Error())
		}
	}
	// Attempt to delete it from GCP.
	if err = d.deletePath(parsedPath); err != nil {
		return newDirError(op, parsedPath, err.Error())
	}
	// If this was an Access file, we need to delete it from the root as well.
	if access.IsAccessFile(parsedPath) {
		err = d.deleteAccess(&parsed)
		if err != nil {
			return err
		}
	}
	if access.IsGroupFile(parsedPath) {
		access.RemoveGroup(parsedPath) // ignore error since it doesn't matter if the group was added already.
	}
	log.Printf("Deleted %s", parsedPath)
	return nil
}

// newStoreClient is a function that creates a store client for an endpoint.
type newStoreClient func(e upspin.Endpoint) (upspin.Store, error)

func newDirServer(cloudClient gcpCloud.GCP, f upspin.Factotum, newStoreClient newStoreClient, timeFunc func() upspin.Time) *dirServer {
	d := &dirServer{
		cloudClient:    cloudClient,
		factotum:       f,
		newStoreClient: newStoreClient,
		timeNow:        timeFunc,
		dirCache:       cache.NewLRU(1000), // TODO: adjust numbers
		rootCache:      cache.NewLRU(1000), // TODO: adjust numbers
		dirNegCache:    cache.NewLRU(1000), // TODO: adjust numbers
	}
	// Use our default if not given one.
	if d.newStoreClient == nil {
		d.newStoreClient = d.newDefaultStoreClient
	}
	// Use the default time function if not given one.
	if d.timeNow == nil {
		d.timeNow = upspin.Now
	}

	return d
}

// newStoreClient is newStoreCllient function that creates a Store object connected to the Store endpoint and loads
// a context for this server (using its factotum for keys).
func (d *dirServer) newDefaultStoreClient(e upspin.Endpoint) (upspin.Store, error) {
	serverContext := upspin.Context{
		UserName: dirServerName,
		Factotum: d.factotum,
	}

	return bind.Store(&serverContext, e)
}

// storeGet binds to the endpoint in the location, calls the store client and resolves up to one indirection,
// returning the contents of the file.
func (d *dirServer) storeGet(loc *upspin.Location) ([]byte, error) {
	store, err := d.newStoreClient(loc.Endpoint)
	if err != nil {
		return nil, newDirError("storeGet", upspin.PathName(loc.Reference), fmt.Errorf("can't create new store client: %s", err).Error())
	}
	data, locs, err := store.Get(loc.Reference)
	if err != nil {
		return nil, err
	}
	if data != nil {
		return data, nil
	}
	if len(locs) > 0 {
		data, _, err := store.Get(locs[0].Reference)
		return data, err
	}
	return data, err
}

var (
	mu       sync.Mutex // protects fields below
	refCount uint64
)

// Dial implements upspin.Service.
func (d *dirServer) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.New("user gcp: unrecognized transport")
	}
	mu.Lock()
	defer mu.Unlock()

	refCount++
	if refCount == 0 {
		// This is virtually impossible to happen. One will run out of memory before this happens.
		// It means the ref count wrapped around and thus we can't handle another instance. Fail.
		refCount--
		return nil, errors.New("user gcp: internal error: refCount wrapped around")
	}

	this := *d              // Clone ourselves.
	this.context = *context // Make a copy of the context, to prevent changes.
	this.endpoint = e
	return &this, nil
}

// Configure configures the connection to the backing store (namely, GCP) once the service
// has been dialed. The details of the configuration are explained at the package comments.
func (s *dirServer) Configure(options ...string) error {
	// These are defaults that only make sense for those running upspin.io.
	bucketName := "g-upspin-directory"
	projectID := "upspin"
	for _, option := range options {
		opts := strings.Split(option, "=")
		if len(opts) != 2 {
			return fmt.Errorf("invalid option format: %q", option)
		}
		switch opts[0] {
		case ConfigBucketName:
			bucketName = opts[1]
		case ConfigProjectID:
			projectID = opts[1]
		default:
			return fmt.Errorf("invalid configuration option: %q", opts[0])
		}
	}

	s.cloudClient = gcpCloud.New(projectID, bucketName, gcpCloud.ProjectPrivate)
	log.Debug.Printf("Configured GCP store: %v", options)
	return nil
}

/*
// isConfigured returns whether this server is configured properly.
func (s *dirServer) isConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cloudClient != nil && s.fileCache != nil
}
*/

// Ping implements upspin.Service.
func (d *dirServer) Ping() bool {
	return true
}

func (d *dirServer) Close() {
	mu.Lock()
	defer mu.Unlock()

	// Clean up this instance
	d.context.UserName = "" // ensure we get an error in subsequent calls.

	refCount--
	if refCount == 0 {
		d.cloudClient = nil
		d.dirCache = nil
		d.dirNegCache = nil
		d.rootCache = nil
		// Do any other global clean ups here.
	}
}

// Authenticate implements upspin.Service.
func (d *dirServer) Authenticate(*upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// ServerUserName implements upspin.Service.
func (d *dirServer) ServerUserName() string {
	return string(d.context.UserName)
}

// Endpoint implements upspin.Service.
func (d *dirServer) Endpoint() upspin.Endpoint {
	return d.endpoint
}

/*
func main() {
	flag.Parse()

	// Somehow the Logging API requires a 'google.com:' prefix, but the GCS buckets do not.
	// Use the bucketname as the logging prefix so we can differentiate the main dir server and the test dir server.
	log.Connect("google.com:"+*projectID, *bucketName)

	ah := httpauth.NewHandler(&auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	})

	ctx := upspin.Context{
		UserName: dirServerName,
	}
	err := keyloader.Load(&ctx) // Keys are now in the server's home dir on upspin.io.
	if err != nil {
		log.Fatal(err)
	}
	d := newDirServer(gcp.New(*projectID, *bucketName, gcp.ProjectPrivate), ctx.Factotum, nil)

	http.HandleFunc("/dir/", ah.Handle(d.dirHandler)) // dir handles GET, PUT/POST and DELETE.
	http.HandleFunc("/glob/", ah.Handle(d.globHandler))
	http.HandleFunc("/whichaccess/", ah.Handle(d.whichAccessHandler))

	if *sslCertificateFile != "" && *sslCertificateKeyFile != "" {
		server, err := httpauth.NewHTTPSecureServer(*port, *sslCertificateFile, *sslCertificateKeyFile)
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
*/
