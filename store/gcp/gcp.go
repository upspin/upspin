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

// server implements upspin.StoreServer.
type server struct {
	ctx     upspin.Context   // server context.
	user    upspin.UserName  // owner of this instance of the server.
	storage storage.Storage  // underlying storage medium.
	cache   *cache.FileCache // local file cache.
}

var _ upspin.StoreServer = (*server)(nil)

// New returns a StoreServer for a context with the provided options.
func New(ctx upspin.Context, options ...string) (upspin.StoreServer, error) {
	const op = "store/gcp.New"

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

	srv := &server{
		ctx:     ctx,
		user:    ctx.UserName(),
		storage: s,
		cache:   c,
	}
	return srv, nil
}

// Put implements upspin.StoreServer.
func (s *server) Put(data []byte) (*upspin.Refdata, error) {
	const op = "store/gcp.Put"

	ref := sha256key.Of(data).String()
	err := s.cache.Put(ref, bytes.NewReader(data))
	if err != nil {
		return nil, errors.E(op, err)
	}

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
	}()
	refdata := &upspin.Refdata{
		Reference: upspin.Reference(ref),
		Volatile:  false,
		Duration:  0,
	}
	return refdata, nil
}

// Get implements upspin.StoreServer.
func (s *server) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	const op = "store/gcp.Get"
	file, loc, err := s.innerGet(ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if file != nil {
		defer file.Close()
		bytes, err := ioutil.ReadAll(file)
		if err != nil {
			err = errors.E(op, err)
		}
		return bytes, nil, nil, err
	}
	refdata := &upspin.Refdata{
		Reference: ref,
		Volatile:  false,
		Duration:  0,
	}
	return nil, refdata, []upspin.Location{loc}, nil
}

// innerGet gets a local file descriptor or a new location for the reference. It returns only one of the two return
// values or an error. file is non-nil when the ref is found locally; the file is open for read and the
// caller should close it. If location is non-zero ref is in the backend at that location.
func (s *server) innerGet(ref upspin.Reference) (file *os.File, location upspin.Location, err error) {
	const op = "store/gcp.Get"
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
		err = errors.E(op, err)
		return
	}
	// GCP should return an http link
	if !strings.HasPrefix(link, "http") {
		err = errors.E(op, errors.Errorf("invalid link returned from GCP: %s", link))
		log.Error.Println(err)
		return
	}

	url, err := url.Parse(link)
	if err != nil {
		err = errors.E(op, errors.Errorf("can't parse url: %s: %s", link, err))
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
	const op = "store/gcp.Delete"
	err := s.storage.Delete(string(ref))
	if err != nil {
		return errors.E(op, errors.Errorf("%s: %s", ref, err))
	}
	return nil
}

// Dial implements upspin.Service.
func (s *server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	newS := *s
	newS.user = context.UserName()
	return &newS, nil
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *server) Close() {
	// TODO: Close is never called in practice so we might as well remove it
	// from the interface.
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return s.ctx.StoreEndpoint()
}
