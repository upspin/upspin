// Package main implements the GCP Store server.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"upspin.io/cloud/gcp"
	"upspin.io/cmd/store/cache"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/upspin"
)

// Common variables uses by all implementations of store.Server.
var (
	ServerName    = "StoreService: "
	ErrInvalidRef = errors.New("invalid reference")
)

// Server implements methods from upspin.Store that are relevant on the server side for both GRPC and HTTP servers.
// All methods from upspin.Store are given the user name making the call. A context could be used for completion,
// but we only care about the user name, so we use that for simplicity.
type server struct {
	cloudClient gcp.GCP
	fileCache   *cache.FileCache
}

// Put implements upspin.Store for a given UserName.
func (s *server) Put(userName upspin.UserName, data []byte) (upspin.Reference, error) {
	return s.innerPut(userName, bytes.NewBuffer(data))
}

// InnerPut implements upspin.Store for a given UserName using an io.Reader.
func (s *server) innerPut(userName upspin.UserName, reader io.Reader) (upspin.Reference, error) {
	// TODO: check that userName has permission to write to this store server.
	sha := sha256key.NewShaReader(reader)
	initialRef := s.fileCache.RandomRef()
	err := s.fileCache.Put(initialRef, sha)
	if err != nil {
		return "", fmt.Errorf("%sPut: %s", ServerName, err)
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
	return upspin.Reference(ref), nil
}

// Get implements upspin.Store for a UserName.
func (s *server) Get(userName upspin.UserName, ref upspin.Reference) ([]byte, []upspin.Location, error) {
	file, loc, err := s.innerGet(userName, ref)
	if err != nil {
		return nil, nil, err
	}
	if file != nil {
		defer file.Close()
		bytes, err := ioutil.ReadAll(file)
		if err != nil {
			err = fmt.Errorf("%sGet: %s", ServerName, err)
		}
		return bytes, nil, err
	}
	return nil, []upspin.Location{loc}, nil
}

// InnerGet is the version of Store.Get that supports both the HTTP interface and the higher level Store.Get
// (which matches one-to-one with the GRPC interface). It returns only one of the two return values or an error.
// file is non-nil when the ref is found locally. If non-nil, the file is open for read and the caller should close it
// (after which it may disappear). If location is non-zero it means ref is in the backend at that location.
func (s *server) innerGet(userName upspin.UserName, ref upspin.Reference) (file *os.File, location upspin.Location, err error) {
	file, err = s.fileCache.OpenRefForRead(string(ref))
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		log.Printf("ref %s is in local cache. Returning it as file: %s", ref, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	var link string
	link, err = s.cloudClient.Get(string(ref))
	if err != nil {
		err = fmt.Errorf("%sGet: %s", ServerName, err)
		return
	}
	// GCP should return an http link
	if !strings.HasPrefix(link, "http") {
		errMsg := fmt.Sprintf("%sGet: invalid link returned from GCP: %s", ServerName, link)
		log.Error.Println(errMsg)
		err = errors.New(errMsg)
		return
	}

	location.Reference = upspin.Reference(link)
	location.Endpoint.Transport = upspin.GCP // Go fetch using the provided link.
	log.Printf("Ref %s returned as link: %s", ref, link)
	return
}

// Delete implements upspin.Store for a UserName. It's common between HTTP and GRPC.
func (s *server) Delete(userName upspin.UserName, ref upspin.Reference) error {
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.cloudClient.Delete(string(ref))
	if err != nil {
		return fmt.Errorf("%sDelete: %s: %s", ServerName, ref, err)
	}
	log.Printf("Delete: %s: Success", ref)
	return nil
}
