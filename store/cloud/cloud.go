// Implements a simple interface with Google Cloud Storage, for storing blobs in buckets
package cloud

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	storage "google.golang.org/api/storage/v1"
)

var (
	projectID  = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
)

const (
	scope = storage.DevstorageFullControlScope
)

type Cloud struct {
	client  *http.Client
	service *storage.Service
}

func New() *Cloud {
	c := &Cloud{}
	c.Connect()
	return c
}

// Puts a blob that is already in the local file system with name 'srcBlobName' into our
// bucket on cloud store. Returns the reference where the blob is stored, which is a SHA512
// of its contents. Returns the reference link to the file directly.
func (c *Cloud) PutBlob(srcBlobName string, sha512str string) (refLink string) {
	// Insert an object into a bucket.
	object := &storage.Object{Name: sha512str}
	file, err := os.Open(srcBlobName)
	if err != nil {
		log.Fatalf("Error opening %q: %v", srcBlobName, err)
	}
	res, err := c.service.Objects.Insert(*bucketName, object).Media(file).PredefinedAcl("publicRead").Do()
	if err == nil {
		fmt.Printf("Created object %v at location %v\n\n", res.Name, res.SelfLink)
	} else {
		log.Fatalf("Objects.Insert failed: %v", err)
	}
	return res.MediaLink
}

// Given a string of the sha512 in base64 representing the blob name, retrieves a link to the
// blob.
func (c *Cloud) GetBlob(sha512str string) (link string, error error) {
	// Get the link of the blob
	res, err := c.service.Objects.Get(*bucketName, sha512str).Do()
	if err != nil {
		return "", err
	}
	fmt.Printf("The media download link for %v/%v is %v.\n\n", *bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

func (c *Cloud) Connect() {
	flag.Parse()
	if *bucketName == "" {
		log.Fatalf("Bucket argument is required. See --help.")
	}
	if *projectID == "" {
		log.Fatalf("Project argument is required. See --help.")
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
	// Initializes the object
	c.client = client
	c.service = service
}
