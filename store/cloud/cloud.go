// Package cloud implements a simple interface with the Google Cloud Storage, for storing blobs in buckets.
package cloud

import (
	"fmt"
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

// Cloud is a client connection with Google Cloud Platform.
type Cloud struct {
	client     *http.Client
	service    *storage.Service
	projectId  string
	bucketName string
}

// New creates a new Cloud instance associated with the given project id and bucket name.
func New(projectId, bucketName string) *Cloud {
	c := &Cloud{}
	c.projectId = projectId
	c.bucketName = bucketName
	c.Connect()
	return c
}

// PutBlob puts a blob that is already in the local file system with
// name 'srcBlobName' into our bucket on the cloud store under the
// given reference 'ref', which is typically a SHA digest of the
// contents of the blob.  It returns a reference link to the blob
// directly.
func (c *Cloud) PutBlob(srcBlobName string, ref string) (refLink string) {
	// Insert an object into a bucket.
	object := &storage.Object{Name: ref}
	file, err := os.Open(srcBlobName)
	if err != nil {
		log.Fatalf("Error opening: %v", err)
	}
	res, err := c.service.Objects.Insert(c.bucketName, object).Media(file).PredefinedAcl("publicRead").Do()
	if err == nil {
		log.Printf("Created object %v at location %v\n", res.Name, res.SelfLink)
	} else {
		log.Fatalf("Objects.Insert failed: %v", err)
	}
	return res.MediaLink
}

// GetBlob returns a link to the blob identified by ref, or an error if the ref is not found.
func (c *Cloud) GetBlob(ref string) (link string, error error) {
	// Get the link of the blob
	res, err := c.service.Objects.Get(c.bucketName, ref).Do()
	if err != nil {
		return "", err
	}
	fmt.Printf("The media download link for %v/%v is %v.\n\n", c.bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

// Connect connects with the Google Cloud Platform, under the given projectId and bucketName.
func (c *Cloud) Connect() {
	if c.projectId == "" {
		log.Fatalf("Project argument is required.")
	}
	if c.bucketName == "" {
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
	c.client = client
	c.service = service
}
