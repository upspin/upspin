// Package gcp implements upspin.Store using Google Cloud Platform as its storage.
package gcp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"strings"
	"sync"

	"upspin.io/bind"
	gcpCloud "upspin.io/cloud/gcp"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/store/gcp/cache"
	"upspin.io/upspin"
)

// Configuration options for this package.
const (
	// ConfigProjectID specifies which GCP project to use for talking to GCP.
	//
	ConfigProjectID = "gcpProjectId"

	// ConfigBucketName specifies which GCS bucket to store data in.
	// If not specified, "g-upspin-store" is used.
	ConfigBucketName = "gcpBucketName"

	// ConfigTemporaryDir specifies which temporary directory to write files to before they're
	// uploaded to the destination bucket. If not present, one will be created in the
	// system's default location.
	ConfigTemporaryDir = "gcpTemporaryDir"
)

// Common variables uses by all implementations of store.Server.
var (
	errorPrefix      = "Store: "
	errNotConfigured = errors.New("not configured")
)

// Server implements upspin.Store.
type server struct {
	context  upspin.Context
	endpoint upspin.Endpoint

	mu          sync.RWMutex // Protects fields below from disappearing.
	cloudClient gcpCloud.GCP
	fileCache   *cache.FileCache
}

var _ upspin.Store = (*server)(nil)

// New returns a new, unconfigured Store bound to the user in the context.
func New(context *upspin.Context) upspin.Store {
	return &server{
		context: *context, // Make a copy to prevent user making further changes.
	}
}

// Put implements upspin.Store.
func (s *server) Put(data []byte) (upspin.Reference, error) {
	log.Printf("Put")
	return s.innerPut(s.context.UserName, bytes.NewBuffer(data))
}

// InnerPut implements upspin.Store for a given UserName using an io.Reader.
func (s *server) innerPut(userName upspin.UserName, reader io.Reader) (upspin.Reference, error) {
	// TODO: check that userName has permission to write to this store server.
	s.mu.RLock()
	if err := s.isConfigured(); err != nil {
		s.mu.RUnlock()
		return "", err
	}
	sha := sha256key.NewShaReader(reader)
	initialRef := s.fileCache.RandomRef()
	err := s.fileCache.Put(initialRef, sha)
	if err != nil {
		s.mu.RUnlock()
		return "", fmt.Errorf("%sPut: %s", errorPrefix, err)
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
		s.mu.RUnlock()
	}()
	return upspin.Reference(ref), nil
}

// Get implements upspin.Store.
func (s *server) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	file, loc, err := s.innerGet(s.context.UserName, ref)
	if err != nil {
		return nil, nil, err
	}
	if file != nil {
		defer file.Close()
		bytes, err := ioutil.ReadAll(file)
		if err != nil {
			err = fmt.Errorf("%sGet: %s", errorPrefix, err)
		}
		return bytes, nil, err
	}
	return nil, []upspin.Location{loc}, nil
}

// innerGet gets a local file descriptor or a new location for the reference. It returns only one of the two return
// values or an error. file is non-nil when the ref is found locally; the file is open for read and the
// caller should close it. If location is non-zero ref is in the backend at that location.
func (s *server) innerGet(userName upspin.UserName, ref upspin.Reference) (file *os.File, location upspin.Location, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var zeroLoc upspin.Location
	if err := s.isConfigured(); err != nil {
		return nil, zeroLoc, err
	}
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
		err = fmt.Errorf("%sGet: %s", errorPrefix, err)
		return
	}
	// GCP should return an http link
	if !strings.HasPrefix(link, "http") {
		errMsg := fmt.Sprintf("%sGet: invalid link returned from GCP: %s", errorPrefix, link)
		log.Error.Println(errMsg)
		err = errors.New(errMsg)
		return
	}

	url, err := url.Parse(link)
	if err != nil {
		errMsg := fmt.Sprintf("%sGet: can't parse url: %s: %s", errorPrefix, link, err)
		log.Error.Print(errMsg)
		err = errors.New(errMsg)
		return
	}
	location.Reference = upspin.Reference(link)
	// Go fetch using the provided link. NetAddr is important so we can both ping the server and also cache the
	// HTTPS transport client efficiently.
	location.Endpoint.Transport = upspin.HTTPS
	location.Endpoint.NetAddr = upspin.NetAddr(fmt.Sprintf("%s://%s", url.Scheme, url.Host))
	log.Printf("Ref %s returned as link: %s", ref, link)
	return
}

// Delete implements upspin.Store for a UserName. It's common between HTTP and GRPC.
func (s *server) Delete(ref upspin.Reference) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.isConfigured(); err != nil {
		return err
	}
	// TODO: verify ownership and proper ACLs to delete blob
	err := s.cloudClient.Delete(string(ref))
	if err != nil {
		return fmt.Errorf("%sDelete: %s: %s", errorPrefix, ref, err)
	}
	log.Printf("Delete: %s: Success", ref)
	return nil
}

func (s *server) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.New("store gcp: unrecognized transport")
	}
	this := *s              // Clone ourselves.
	this.context = *context // Make a copy of the context, to prevent changes.
	this.endpoint = e
	return &this, nil
}

// Configure configures the connection to the backing store (namely, GCP) once the service
// has been dialed. The details of the configuration are explained at the package comments.
func (s *server) Configure(options ...string) error {
	if len(options)%2 != 0 {
		return errors.New("incorrect number of configuration options")
	}
	// These are defaults that only make sense for those running upspin.io.
	bucketName := "g-upspin-store"
	projectID := "upspin"
	tempDir := ""
	for i := 0; i < len(options); i += 2 {
		opt := options[i]
		switch opt {
		case ConfigBucketName:
			bucketName = options[i+1]
		case ConfigProjectID:
			projectID = options[i+1]
		case ConfigTemporaryDir:
			tempDir = options[i+1]
		default:
			return fmt.Errorf("invalid configuration option: %s", opt)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cloudClient = gcpCloud.New(projectID, bucketName, gcpCloud.PublicRead)
	s.fileCache = cache.NewFileCache(tempDir)
	if s.fileCache == nil {
		return errors.New("filecache failed to create temp directory")
	}
	log.Printf("Configured GCP store!")
	return nil
}

// returns an error if the service has not been configured yet.
func (s *server) isConfigured() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cloudClient == nil || s.fileCache == nil {
		return errNotConfigured
	}
	return nil
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cloudClient = nil
	s.fileCache.Delete()
	s.fileCache = nil
}

// Authenticate implements upspin.Service.
func (s *server) Authenticate(*upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// ServerUserName implements upspin.Service.
func (s *server) ServerUserName() string {
	return string(s.context.UserName)
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func init() {
	bind.RegisterStore(upspin.GCP, New(&upspin.Context{}))
}
