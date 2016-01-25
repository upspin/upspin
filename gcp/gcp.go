// Package gcp implements a simple interface with the Google Cloud Platform,
// for storing blobs in buckets and performing other type of maintenange on GCP.
package gcp

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	storage "google.golang.org/api/storage/v1"
)

const (
	scope = storage.DevstorageFullControlScope
)

// ACLs for writing data to Cloud Store.
// Definitions according to https://github.com/google/google-api-go-client/blob/master/storage/v1/storage-gen.go:
//   "authenticatedRead" - Project team owners get OWNER access, and
//       allAuthenticatedUsers get READER access.
//   "private" - Project team owners get OWNER access.
//   "projectPrivate" - Project team members get access according to
//       their roles.
//   "publicRead" - Project team owners get OWNER access, and allUsers
//       get READER access.
//   "publicReadWrite" - Project team owners get OWNER access, and
//       allUsers get WRITER access.
type WriteACL string

const (
	PublicRead        WriteACL = "publicRead"
	AuthenticatedRead WriteACL = "authenticatedRead"
	Private           WriteACL = "private"
	ProjectPrivate    WriteACL = "projectPrivate"
	PublicReadWrite   WriteACL = "publicReadWrite"
	DefaultWriteACL   WriteACL = PublicRead
)

// GCP is a client connection with Google Cloud Platform.
type GCP struct {
	client          *http.Client
	service         *storage.Service
	projectId       string
	bucketName      string
	defaultWriteACL WriteACL
}

// New creates a new GCP instance associated with the given project id and bucket name.
func New(projectId, bucketName string, defaultWriteACL WriteACL) *GCP {
	gcp := &GCP{}
	gcp.projectId = projectId
	gcp.bucketName = bucketName
	switch defaultWriteACL {
	case PublicRead, AuthenticatedRead, Private, ProjectPrivate, PublicReadWrite:
		gcp.defaultWriteACL = defaultWriteACL
	default:
		gcp.defaultWriteACL = DefaultWriteACL
	}
	gcp.Connect()
	return gcp
}

// PutLocalFile puts the contents of a file that is already in the
// local file system with name 'srcLocalFilename' into our bucket on
// the cloud store under the given name 'ref', which can be any string
// or directory path (in case of the Store server it is a SHA digest
// of the contents of the file).  It returns a reference link to the
// stored file directly in case of success, otherwise it sets error to
// non-nil.
func (gcp *GCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// Insert an object into a bucket.
	object := &storage.Object{Name: ref}
	file, err := os.Open(srcLocalFilename)
	if err != nil {
		log.Printf("Error opening: %v", err)
		return "", err
	}
	acl := string(gcp.defaultWriteACL)
	res, err := gcp.service.Objects.Insert(gcp.bucketName, object).Media(file).PredefinedAcl(acl).Do()
	if err == nil {
		log.Printf("Created object %v at location %v\n", res.Name, res.SelfLink)
	} else {
		log.Printf("Objects.Insert failed: %v", err)
	}
	return res.MediaLink, err
}

// Get returns a direct link to the ref on cloud store, or an error if the ref is not found.
func (gcp *GCP) Get(ref string) (link string, error error) {
	// Get the link of the blob
	res, err := gcp.service.Objects.Get(gcp.bucketName, ref).Do()
	if err != nil {
		return "", err
	}
	fmt.Printf("The media download link for %v/%v is %v.\n\n", gcp.bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

// Put stores the contents into the given ref on our bucket on cloud
// store. It returns a link to the permanent location on of the ref on
// cloud store or an error if it couldn't be stored.
func (gcp *GCP) Put(ref string, contents []byte) (refLink string, error error) {
	// TODO(edpin): this is not super safe, given the file has
	// permissions 0666. But for now the contents are always
	// public on cloud store, so it doesn't matter.
	f, err := ioutil.TempFile("", "upload-gcp-blob-")
	if err != nil {
		return "", err
	}
	n, err := f.Write(contents)
	if err != nil || n != len(contents) {
		return "", err
	}
	name := f.Name()
	link, err := gcp.PutLocalFile(name, ref)
	os.Remove(name)
	return link, err
}

// Connect connects with the Google Cloud Platform, under the given projectId and bucketName.
func (gcp *GCP) Connect() {
	if gcp.projectId == "" {
		log.Fatalf("Project argument is required.")
	}
	if gcp.bucketName == "" {
		log.Fatalf("Bucket argument is required.")
	}

	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(context.Background(), scope)
	if err != nil {
		log.Fatalf("Unable to get default client: %v", err)
	}
	service, err := storage.New(client)
	if err != nil {
		log.Fatalf("Unable to create storage service: %v", err)
	}
	// Initialize the object
	gcp.client = client
	gcp.service = service
}
