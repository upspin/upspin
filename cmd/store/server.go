package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
	"upspin.googlesource.com/upspin.git/key/sha256key"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Common variables uses by all implementations of store.Server.
var (
	ServerName    = "StoreService: "
	ErrInvalidRef = errors.New("invalid reference")
)

// Server implements methods from upspin.Store that are relevant on the server side for both GRPC and HTTP servers.
// All methods from upspin.Store are prefixed with the user name. A context could be used for completion,
// but we only care about the user name, so use that for simplicity.
type Server struct {
	CloudClient gcp.GCP
	FileCache   *cache.FileCache
}

// Put implements upspin.Store for a given UserName.
func (s *Server) Put(userName upspin.UserName, data []byte) (upspin.Reference, error) {
	return s.InnerPut(userName, bytes.NewBuffer(data))
}

// InnerPut implements upspin.Store for a given UserName using an io.Reader.
func (s *Server) InnerPut(userName upspin.UserName, reader io.Reader) (upspin.Reference, error) {
	// TODO: check that userName has permission to write to this store server.
	sha := sha256key.NewShaReader(reader)
	initialRef := s.FileCache.RandomRef()
	err := s.FileCache.Put(initialRef, sha)
	if err != nil {
		return "", fmt.Errorf("%sPut: %s", ServerName, err)
	}
	// Figure out the appropriate reference for this blob
	ref := sha.EncodedSum()

	// Rename it in the cache
	s.FileCache.Rename(ref, initialRef)

	// Now go store it in the cloud.
	go func() {
		if _, err := s.CloudClient.PutLocalFile(s.FileCache.GetFileLocation(ref), ref); err == nil {
			// Remove the locally-cached entry so we never
			// keep files locally, as we're a tiny server
			// compared with our much better-provisioned
			// storage backend.  This is safe to do
			// because FileCache is thread safe.
			s.FileCache.Purge(ref)
		}
	}()
	return upspin.Reference(ref), nil
}

// Get implements upspin.Store for a UserName
func (s *Server) Get(userName upspin.UserName, ref upspin.Reference) ([]byte, []upspin.Location, error) {
	file, loc, err := s.InnerGet(userName, ref)
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
func (s *Server) InnerGet(userName upspin.UserName, ref upspin.Reference) (file *os.File, location upspin.Location, err error) {
	file, err = s.FileCache.OpenRefForRead(string(ref))
	if err == nil {
		// Ref is in the local cache. Send the file and be done.
		log.Printf("ref %s is in local cache. Returning it as file: %s", ref, file.Name())
		return
	}

	// File is not local, try to get it from our storage.
	var link string
	link, err = s.CloudClient.Get(string(ref))
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
func (s *Server) Delete(userName upspin.UserName, ref upspin.Reference) error {
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.CloudClient.Delete(string(ref))
	if err != nil {
		return fmt.Errorf("%sDelete: %s: %s", ServerName, ref, err)
	}
	log.Printf("Delete: %s: Success", ref)
	return nil
}
