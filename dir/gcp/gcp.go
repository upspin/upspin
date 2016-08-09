// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements upspin.DirServer for talking to the Google Cloud Platofrm (GCP).
package gcp

import (
	goPath "path"
	"strings"
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/cloud/storage"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful backends
	_ "upspin.io/cloud/storage/gcs"
	_ "upspin.io/cloud/storage/mysql"
	_ "upspin.io/cloud/storage/postgres"
)

type directory struct {
	context  upspin.Context
	endpoint upspin.Endpoint

	// TODO: can one address space be configured to talk to multiple GCP backends and/or act as different directory
	// service users, with different keys? If yes, then Configure should configure the caches too.
	// If not, then these should be singletons.
	timeNow        func() upspin.Time // returns the current time.
	cloudClient    storage.Storage    // handle for GCP bucket g-upspin-directory.
	serverName     upspin.UserName    // this server's user name (for talking to StoreServer, etc).
	factotum       upspin.Factotum    // this server's factotum with its keys.
	newStoreClient newStoreClient     // how to create a StoreServer client.
	dirCache       *cache.LRU         // caches <upspin.PathName, upspin.DirEntry>. It is thread safe.
	rootCache      *cache.LRU         // caches <upspin.UserName, root>. It is thread safe.
	dirNegCache    *cache.LRU         // caches the absence of a path <upspin.PathName, nil>. It is thread safe.
}

var _ upspin.DirServer = (*directory)(nil)

// gcpDir represents a name for the metric for this directory service.
const gcpDir = "gcpDir"

// Configuration options.
const (
	// storageType specified which storage backend to use. Current values are "storage=GCS" or "storage=Postgres".
	storageType = "storage"
)

var (
	errNotConfigured = errors.Str("GCP DirServer not configured")

	confLock sync.RWMutex // protects all configuration options and the ref count below.
	refCount uint64
)

// options are optional parameters to almost every inner method of directory for doing some
// optional, non-correctness-related work.
type options struct {
	span *metric.Span
	// Add other things below (for example, some healthz monitoring stats)
}

// verifyDirEntry checks that the DirEntry is minimally valid.
func verifyDirEntry(op string, d *upspin.DirEntry) error {
	if d.Sequence < upspin.SeqNotExist {
		return errors.E(op, d.Name, errors.Invalid, errors.Str("invalid sequence number"))
	}
	return nil
}

func newOptsForMetric(op string) (options, *metric.Metric) {
	m := metric.New(gcpDir)
	opts := options{
		span: m.StartSpan(op),
	}
	return opts, m
}

// MakeDirectory implements upspin.DirServer.
func (d *directory) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "MakeDirectory"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return nil, errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()

	parsed, err := path.Parse(dirName)
	if err != nil {
		return nil, errors.E(op, err) // path.Parse already populates the path name.
	}
	// Prepares a dir entry for storage.
	dirEntry := &upspin.DirEntry{
		Name:     parsed.Path(),
		Attr:     upspin.AttrDirectory,
		Sequence: 0, // don't care?
		Time:     d.timeNow(),
		Packdata: nil,
		Packing:  upspin.PlainPack,
		Blocks: []upspin.DirBlock{
			{
				Location: upspin.Location{
					// Reference is ignored.
					// Endpoint for dir entries where the DirServer is.
					Endpoint: d.endpoint,
				},
				Size:   0, // Being explicit that dir entries have zero size.
				Offset: 0,
			},
		},
	}
	err = d.put(op, dirEntry, opts)
	if err != nil {
		return nil, err
	}
	return dirEntry, nil
}

// Put writes or overwrites a complete dirEntry to the back end, provided several checks have passed first.
// It implements upspin.DirServer.
func (d *directory) Put(dirEntry *upspin.DirEntry) error {
	const op = "Put"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()
	return d.put(op, dirEntry, opts)
}

// span returns the first span found in opts or a new one if not found.
func span(opts []options) *metric.Span {
	for _, o := range opts {
		if o.span != nil {
			return o.span
		}
	}
	// This is probably an error. Metrics should be created at the entry points only.
	return metric.New("FIXME").StartSpan("FIXME")
}

// isErrNotExist reports whether the error is of type errors.NotExist.
func isErrNotExist(err error) bool {
	e, ok := err.(*errors.Error)
	return ok && e.Kind == errors.NotExist
}

// put is the common implementation between MakeDirectory and Put.
func (d *directory) put(op string, dirEntry *upspin.DirEntry, opts ...options) error {
	s := span(opts)
	ss := s.StartSpan("put")
	defer ss.End()

	parsed, err := path.Parse(dirEntry.Name) // canonicalizes dirEntry.Name
	if err != nil {
		return errors.E(op, dirEntry.Name, err)
	}

	// Lock the user root
	mu := userLock(parsed.User())
	mu.Lock()
	defer mu.Unlock()

	if err := verifyDirEntry(op, dirEntry); err != nil {
		return err
	}

	user := d.context.UserName()
	// If we're creating the root, handle it elsewhere.
	if parsed.IsRoot() {
		// We handle root elsewhere because otherwise this code would be riddled with "if IsRoot..."
		return d.handleRootCreation(user, &parsed, dirEntry, opts...)
	}

	// Check ACLs before we go any further, so we don't leak information about the existence of files and directories.
	canCreate, err := d.hasRight(op, user, access.Create, &parsed, opts...)
	if err != nil {
		return err
	}
	canWrite, err := d.hasRight(op, user, access.Write, &parsed, opts...)
	if err != nil {
		return err
	}
	if dirEntry.IsDir() && !canCreate || !dirEntry.IsDir() && !canWrite {
		return errors.E(op, dirEntry.Name, errors.Permission)
	}

	canonicalPath := parsed.Path()
	parentParsedPath := parsed.Drop(1) // Can't fail as parsed is NOT root.

	// Verify whether there's a directory with same name.
	existingDirEntry, err := d.getNonRoot(canonicalPath, opts...)
	if err != nil && !isErrNotExist(err) {
		return errors.E(op, err)
	}
	if err == nil {
		if existingDirEntry.IsDir() {
			return errors.E(op, canonicalPath, errors.Exist, errors.Str("directory already exists"))
		}
		if dirEntry.IsDir() {
			return errors.E(op, canonicalPath, errors.NotDir, errors.Str("overwriting file with directory"))
		}
		if dirEntry.Sequence == upspin.SeqNotExist {
			return errors.E(op, canonicalPath, errors.Exist, errors.Str("file already exists"))
		}
		if dirEntry.Sequence > upspin.SeqIgnore && dirEntry.Sequence != existingDirEntry.Sequence {
			return errors.E(op, canonicalPath, errors.Invalid, errors.Str("sequence mismatch"))
		}
		dirEntry.Sequence = existingDirEntry.Sequence + 1
	}
	if dirEntry.Sequence < upspin.SeqBase {
		dirEntry.Sequence = upspin.SeqBase
	}

	// Canonicalize path.
	dirEntry.Name = canonicalPath

	// Patch the parent: bump sequence number.
	parentDirEntry, root, err := d.getDirEntry(&parentParsedPath, opts...)
	if err != nil {
		if isErrNotExist(err) {
			return errors.E(op, canonicalPath, errors.NotExist, errors.Str("parent path not found"))
		}
		return err
	}
	// For self-consistency, verify parent really is a directory
	if !parentDirEntry.IsDir() {
		err = errors.E(op, canonicalPath, errors.NotDir, errors.Str("parent is not a directory"))
		log.Error.Printf("Bad inconsistency: %s", err)
		return err
	}
	parentDirEntry.Sequence++
	if parentParsedPath.IsRoot() {
		err = d.putRoot(parentParsedPath.User(), root, opts...)
	} else {
		err = d.putNonRoot(parentParsedPath.Path(), parentDirEntry, opts...)
	}

	// Finally, store the new entry.
	err = d.putNonRoot(canonicalPath, dirEntry, opts...)
	if err != nil {
		return err
	}

	// If this is an Access file or Group file, we have some extra work to do.
	if access.IsAccessFile(canonicalPath) {
		loc := &upspin.Location{}
		if len(dirEntry.Blocks) > 0 {
			loc = &dirEntry.Blocks[0].Location
		}
		err = d.updateAccess(&parsed, loc, opts...)
		if err != nil {
			return err
		}
	}
	if access.IsGroupFile(canonicalPath) {
		log.Debug.Printf("Invalidating group file %s", canonicalPath)
		// By removing the group we guarantee we won't be using its old definition, if any.
		// Since we parse groups lazily, this is correct and generally efficient.
		_ = access.RemoveGroup(canonicalPath) // error is ignored on purpose. If group was not there, no harm done.
	}

	log.Info.Printf("%s: %q %q", op, user, dirEntry.Name)
	return nil
}

// Lookup implements upspin.DirServer.
func (d *directory) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "Lookup"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return nil, errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, pathName, err)
	}

	mu := userLock(parsed.User())
	mu.Lock()
	defer mu.Unlock()

	// Check ACLs before attempting to read the dirEntry to avoid leaking information about the existence of paths.
	canRead, err := d.hasRight(op, d.context.UserName(), access.Read, &parsed, opts)
	if err != nil {
		log.Debug.Printf("Access error Read: %s", err)
		return nil, errors.E(op, err)
	}
	canList, err := d.hasRight(op, d.context.UserName(), access.List, &parsed, opts)
	if err != nil {
		log.Debug.Printf("Access error List: %s", err)
		return nil, errors.E(op, err)
	}
	// If the user has no rights, we're done.
	if !canRead && !canList {
		return nil, errors.E(op, parsed.Path(), errors.Permission)
	}
	// Look up entry
	var dirEntry *upspin.DirEntry
	if !parsed.IsRoot() {
		dirEntry, err = d.getNonRoot(parsed.Path(), opts)
	} else {
		root, err := d.getRoot(parsed.User(), opts)
		if err == nil {
			dirEntry = &root.dirEntry
		}
	}
	if err != nil {
		return nil, errors.E(op, err)
	}
	// We have a dirEntry and ACLs check. But we still must clear Location if user does not have Read rights.
	if !canRead {
		log.Debug.Printf("Zeroing out location information in Get for user %s on path %s", d.context.UserName(), parsed)
		dirEntry.Blocks[0].Location = upspin.Location{}
		dirEntry.Packdata = nil
	}
	log.Debug.Printf("Got dir entry for user %s: path %s: %v", d.context.UserName(), parsed.Path(), dirEntry)
	return dirEntry, nil
}

func (d *directory) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	const op = "WhichAccess"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return "", errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()
	parsed, err := path.Parse(pathName)
	if err != nil {
		return "", errors.E(op, pathName, err)
	}

	mu := userLock(parsed.User())
	mu.Lock()
	defer mu.Unlock()

	user := d.context.UserName()

	// Check ACLs before attempting to look up the Access file to avoid leaking information about the existence of paths.
	canKnow, err := d.hasRight(op, user, access.AnyRight, &parsed, opts)
	if err != nil {
		log.Debug.Printf("WhichAccess error List: %s", err)
		return "", errors.E(op, user, err)
	}
	// If the user has no rights, we're done, but don't tell user the path is valid.
	if !canKnow {
		return "", errors.E(op, parsed.Path(), errors.NotExist)
	}

	accessPath, _, err := d.whichAccess(op, &parsed, opts)
	if err != nil {
		return "", errors.E(op, user, err)
	}
	return accessPath, nil
}

func (d *directory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "Glob"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return nil, errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()

	pathName := upspin.PathName(pattern)
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, pathName, err)
	}

	mu := userLock(parsed.User())
	mu.Lock()
	defer mu.Unlock()

	// Check if pattern is a valid go path pattern
	_, err = goPath.Match(parsed.FilePath(), "")
	if err != nil {
		return nil, errors.E(op, parsed.Path(), err)
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

	ss := opts.span.StartSpan("List")
	var names []string
	if depth == 1 {
		if !strings.HasSuffix(prefix, "/") {
			prefix = prefix + "/"
		}
		names, err = d.cloudClient.ListDir(prefix)
	} else {
		names, err = d.cloudClient.ListPrefix(prefix, int(depth))
	}
	ss.End()
	if err != nil {
		return nil, err
	}

	user := d.context.UserName()
	dirEntries := make([]*upspin.DirEntry, 0, len(names))
	// Now do the actual globbing.
	for _, lookupPath := range names {
		// error is ignored as pattern is known valid
		if match, _ := goPath.Match(parsed.String(), lookupPath); match {
			// Now fetch each DirEntry we need
			log.Debug.Printf("Looking up: %s for glob %s", lookupPath, parsed.String())
			de, err := d.getNonRoot(upspin.PathName(lookupPath), opts)
			if err != nil {
				return nil, err
			}
			// Verify if user has proper list ACL.
			parsedDirName, err := path.Parse(de.Name)
			if err != nil {
				log.Error.Printf("Internal inconsistency: dir entry name does not parse: %s", err)
				continue
			}
			canList, err := d.hasRight(op, user, access.List, &parsedDirName, opts)
			if err != nil {
				log.Error.Printf("Error checking access for user: %s on %s: %s", user, de.Name, err)
				continue
			}
			canRead, err := d.hasRight(op, user, access.Read, &parsedDirName, opts)
			if err != nil {
				log.Error.Printf("Error checking access for user: %s on %s: %s", user, de.Name, err)
				continue
			}
			if !canRead && !canList {
				log.Debug.Printf("User %s can't Glob %s", user, de.Name)
				continue
			}
			// If the user can't read a path, clear out its Location.
			if !canRead {
				if len(de.Blocks) > 0 {
					de.Blocks[0].Location = upspin.Location{}
				}
				de.Packdata = nil
			}
			dirEntries = append(dirEntries, de)
		}
	}
	return dirEntries, nil
}

// deleteDirEntry handles deleting names and their associated DirEntry.
func (d *directory) Delete(pathName upspin.PathName) error {
	const op = "Delete"

	confLock.RLock()
	defer confLock.RUnlock()
	if !d.isConfigured() {
		return errNotConfigured
	}
	opts, m := newOptsForMetric(op)
	defer m.Done()
	parsed, err := path.Parse(pathName)
	if err != nil {
		return err
	}

	mu := userLock(parsed.User())
	mu.Lock()
	defer mu.Unlock()

	user := d.context.UserName()
	// Check ACLs before attempting to get the dirEntry to avoid leaking information about the existence of paths.
	canDelete, err := d.hasRight(op, user, access.Delete, &parsed, opts)
	if err != nil {
		log.Error.Printf("Access error for Delete: %s", err)
		return err
	}
	if !canDelete {
		return errors.E(op, parsed.Path(), errors.Permission)
	}

	// Locate the entry first.
	dirEntry, _, err := d.getDirEntry(&parsed, opts)
	if err != nil {
		return err
	}

	parsedPath := parsed.Path()
	// Only empty directories can be removed.
	if dirEntry.IsDir() {
		err = d.isDirEmpty(parsedPath)
		if err != nil {
			return errors.E(op, err)
		}
	}
	// Attempt to delete it from GCP.
	if err = d.deletePath(parsedPath); err != nil {
		return errors.E(op, err)
	}
	// If this was an Access file, we need to delete it from the root as well.
	if access.IsAccessFile(parsedPath) {
		err = d.deleteAccess(&parsed)
		if err != nil {
			return errors.E(op, err)
		}
	}
	if access.IsGroupFile(parsedPath) {
		access.RemoveGroup(parsedPath) // ignore error since it doesn't matter if the group was added already.
	}
	log.Debug.Printf("Deleted %s", parsedPath)
	return nil
}

// newStoreClient is a function that creates a store client for an endpoint.
type newStoreClient func(e upspin.Endpoint) (upspin.StoreServer, error)

func newDirectory(cloudClient storage.Storage, f upspin.Factotum, newStoreClient newStoreClient, timeFunc func() upspin.Time) *directory {
	d := &directory{
		context:        context.New(),
		cloudClient:    cloudClient,
		factotum:       f,
		newStoreClient: newStoreClient,
		timeNow:        timeFunc,
		dirCache:       cache.NewLRU(1000), // TODO: adjust numbers
		rootCache:      cache.NewLRU(1000), // TODO: adjust numbers
		dirNegCache:    cache.NewLRU(1000), // TODO: adjust numbers
	}
	// Use the default time function if not given one.
	if d.timeNow == nil {
		d.timeNow = upspin.Now
	}

	return d
}

// newStoreClient is newStoreCllient function that creates a StoreServer instance connected to the StoreServer endpoint and loads
// a context for this server (using its factotum for keys).
func (d *directory) newDefaultStoreClient(e upspin.Endpoint) (upspin.StoreServer, error) {
	serverContext := context.New().SetFactotum(d.factotum).SetUserName(d.serverName)
	return bind.StoreServer(serverContext, e)
}

// storeGet binds to the endpoint in the location, calls the store client and resolves up to one indirection,
// returning the contents of the file.
func (d *directory) storeGet(loc *upspin.Location) ([]byte, error) {
	var store upspin.StoreServer
	var err error
	// Use our default if not given one.
	if d.newStoreClient == nil {
		store, err = d.newDefaultStoreClient(loc.Endpoint)
	} else {
		store, err = d.newStoreClient(loc.Endpoint)
	}
	if err != nil {
		return nil, errors.E("storeGet", upspin.PathName(loc.Reference), "can't create new store client", err)
	}
	log.Debug.Printf("storeGet: going to get loc: %v", loc)
	data, locs, err := store.Get(loc.Reference)
	if err != nil {
		return nil, err
	}
	if data != nil {
		return data, nil
	}
	if len(locs) > 0 {
		// TODO: this only does one redirection. It also might recurse forever if the redirections refer to each other.
		return d.storeGet(&locs[0])
	}
	return data, err
}

// Dial implements upspin.Service.
func (d *directory) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.E("Dial", errors.Syntax, errors.Str("unrecognized transport"))
	}
	confLock.Lock()
	defer confLock.Unlock()

	refCount++
	if refCount == 0 {
		// This is virtually impossible to happen. One will run out of memory before this happens.
		// It means the ref count wrapped around and thus we can't handle another instance. Fail.
		refCount--
		return nil, errors.E("Dial", errors.Other, errors.Str("refCount wrapped around"))
	}

	this := *d                    // Clone ourselves.
	this.context = context.Copy() // Make a copy of the context, to prevent changes.
	this.endpoint = e

	// Did we inherit keys and a service username from a generator instance (the instance that was
	// registered originally with Bind)? If not, set them up now.

	// Have we keys for this service already?
	if this.factotum == nil {
		this.factotum = context.Factotum()
	}
	// Have we a server name already?
	if this.serverName == "" {
		this.serverName = context.UserName()
	}

	return &this, nil
}

// Configure configures the connection to the backing store (namely, GCP) once the service
// has been dialed. The details of the configuration are explained at the package comments.
func (d *directory) Configure(options ...string) error {
	const Configure = "Configure"
	var storageBackend string
	var dialOpts []storage.DialOpts
	for _, option := range options {
		// Parse options that we understand, otherwise pass it down to the storage layer.
		switch {
		case strings.Contains(option, storageType):
			storageBackend = option[len(storageType)+1:]
		default:
			dialOpts = append(dialOpts, storage.WithOptions(option))
		}
	}
	if storageBackend == "" {
		return errors.E(Configure, errors.Syntax, errors.Str("must specify storage type"))
	}

	confLock.Lock()
	defer confLock.Unlock()

	var err error
	d.cloudClient, err = storage.Dial(storageBackend, dialOpts...)
	if err != nil {
		return errors.E(Configure, err)
	}
	log.Debug.Printf("Configured GCP directory: %v", options)
	return nil
}

// isConfigured returns whether this server is configured properly.
// It must be called with mu locked.
func (d *directory) isConfigured() bool {
	return d.cloudClient != nil && d.context.UserName() != ""
}

// Ping implements upspin.Service.
func (d *directory) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (d *directory) Close() {
	confLock.Lock()
	defer confLock.Unlock()

	// Clean up this instance
	d.context.SetUserName("") // ensure we get an error in subsequent calls.

	refCount--
	if refCount == 0 {
		d.cloudClient.Close()
		d.cloudClient = nil
		d.dirCache = nil
		d.dirNegCache = nil
		d.rootCache = nil
		// Do any other global clean ups here.
	}
}

// Authenticate implements upspin.Service.
func (d *directory) Authenticate(upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// Endpoint implements upspin.Service.
func (d *directory) Endpoint() upspin.Endpoint {
	return d.endpoint
}

func init() {
	bind.RegisterDirServer(upspin.GCP, newDirectory(nil, nil, nil, nil))
}
