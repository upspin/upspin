// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements upspin.StoreServer using Google Cloud Platform as its storage.
package gcp

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"strings"
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/store/gcp/cache"
	"upspin.io/upspin"

	// We use GCS as the backing for our data.
	_ "upspin.io/cloud/storage/gcs"
)

// Configuration options for this package.
const (
	// ConfigTemporaryDir specifies which temporary directory to write files to before they're
	// uploaded to the destination bucket. If not present, one will be created in the
	// system's default location.
	ConfigTemporaryDir = "gcpTemporaryDir"
)

var (
	errNotConfigured = errors.E(errors.Invalid, errors.Str("GCP StoreServer not configured"))
)

// server implements upspin.StoreServer.
type server struct {
	mu       sync.RWMutex // Protects fields below.
	refCount uint64       // How many clones of us exist.
	storage  storage.Storage
	cache    *cache.FileCache
}

var _ upspin.StoreServer = (*server)(nil)

// New returns a StoreServer that serves the given endpoint with the provided options.
func New(options ...string) (upspin.StoreServer, error) {
	const op = "gcp.New"

	var dialOpts []storage.DialOpts
	var tempDir string
	for _, option := range options {
		// Parse all options we understand.
		// What we don't understand we pass it down to the storage.
		switch {
		case strings.HasPrefix(option, ConfigTemporaryDir):
			tempDir = option[len(ConfigTemporaryDir)+1:] // skip 'ConfigTemporaryDir='
		default:
			dialOpts = append(dialOpts, storage.WithOptions(option))
		}
	}

	s, err := storage.Dial("GCS", dialOpts...)
	if err != nil {
		return nil, errors.E(op, err)
	}
	c := cache.NewFileCache(tempDir)
	if c == nil {
		return nil, errors.E(op, errors.Str("filecache failed to create temp directory"))
	}
	log.Debug.Printf("Configured GCP store: %v", options)

	return &server{
		storage: s,
		cache:   c,
	}, nil
}

// Put implements upspin.StoreServer.
func (s *server) Put(data []byte) (upspin.Reference, error) {
	const Put = "Put"
	reader := bytes.NewReader(data)
	// TODO: check that userName has permission to write to this store server.
	s.mu.RLock()
	sha := sha256key.NewShaReader(reader)
	initialRef := s.cache.RandomRef()
	err := s.cache.Put(initialRef, sha)
	if err != nil {
		s.mu.RUnlock()
		return "", errors.E(Put, err)
	}
	// Figure out the appropriate reference for this blob.
	ref := sha.EncodedSum()

	// Rename it in the cache
	s.cache.Rename(ref, initialRef)

	// Now go store it in the cloud.
	go func() {
		if _, err := s.storage.PutLocalFile(s.cache.GetFileLocation(ref), ref); err == nil {
			// Remove the locally-cached entry so we never
			// keep files locally, as we're a tiny server
			// compared with our much better-provisioned
			// storage backend.  This is safe to do
			// because FileCache is thread safe.
			s.cache.Purge(ref)
		}
		s.mu.RUnlock()
	}()
	return upspin.Reference(ref), nil
}

// Get implements upspin.StoreServer.
func (s *server) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	file, loc, err := s.innerGet(ref)
	if err != nil {
		return nil, nil, err
	}
	if file != nil {
		defer file.Close()
		bytes, err := ioutil.ReadAll(file)
		if err != nil {
			err = errors.E("Get", err)
		}
		return bytes, nil, err
	}
	return nil, []upspin.Location{loc}, nil
}

// innerGet gets a local file descriptor or a new location for the reference. It returns only one of the two return
// values or an error. file is non-nil when the ref is found locally; the file is open for read and the
// caller should close it. If location is non-zero ref is in the backend at that location.
func (s *server) innerGet(ref upspin.Reference) (file *os.File, location upspin.Location, err error) {
	const Get = "Get"
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err = s.cache.OpenRefForRead(string(ref))
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		log.Debug.Printf("ref %s is in local cache. Returning it as file: %s", ref, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	var link string
	link, err = s.storage.Get(string(ref))
	if err != nil {
		err = errors.E(Get, err)
		return
	}
	// GCP should return an http link
	if !strings.HasPrefix(link, "http") {
		err = errors.E(Get, errors.Errorf("invalid link returned from GCP: %s", link))
		log.Error.Println(err)
		return
	}

	url, err := url.Parse(link)
	if err != nil {
		err = errors.E(Get, errors.Errorf("can't parse url: %s: %s", link, err))
		log.Error.Print(err)
		return
	}
	location.Reference = upspin.Reference(link)
	// Go fetch using the provided link. NetAddr is important so we can both ping the server and also cache the
	// HTTPS transport client efficiently.
	location.Endpoint.Transport = upspin.HTTPS
	location.Endpoint.NetAddr = upspin.NetAddr(fmt.Sprintf("%s://%s", url.Scheme, url.Host))
	log.Debug.Printf("Ref %s returned as link: %s", ref, link)
	return
}

// Delete implements upspin.StoreServer.
func (s *server) Delete(ref upspin.Reference) error {
	const Delete = "Delete"
	s.mu.RLock()
	defer s.mu.RUnlock()
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.storage.Delete(string(ref))
	if err != nil {
		return errors.E(Delete, errors.Errorf("%s: %s", ref, err))
	}
	return nil
}

// Dial implements upspin.Service.
func (s *server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refCount++
	return s, nil
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refCount == 0 {
		log.Error.Printf("Closing non-dialed gcp store")
		return
	}
	s.refCount--

	if s.refCount == 0 {
		if s.storage != nil {
			s.storage.Close()
		}
		s.storage = nil
		if s.cache != nil {
			s.cache.Delete()
		}
		s.cache = nil
	}
}

// Authenticate implements upspin.Service.
func (s *server) Authenticate(upspin.Context) error {
	return errors.Str("store/gcp: Authenticate should not be called")
}

// Configure implements upspin.Service.
func (s *server) Configure(options ...string) error {
	return errors.Str("store/gcp: Configure should not be called")
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{} // No endpoint.
}
