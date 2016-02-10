// Package gcp implements a simple interface with the Google Cloud Platform
// for storing blobs in buckets and performing other types of maintenance on GCP.
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

// Interface is how GCP clients talk to GCP.
type Interface interface {
	// PutLocalFile copies a local file to GCP using ref as its
	// name. It returns a direct link for downloading the file
	// from GCP.
	PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error)

	// Get returns a link for downloading ref from GCP.
	Get(ref string) (link string, error error)

	// Put stores the contents given as ref on GCP.
	Put(ref string, contents []byte) (refLink string, error error)

	// List returns all the filenames stored inside a given path
	// prefix.  If successful, it returns two parallel slices
	// containing for each file its name and a URL-encoded link to
	// it.
	List(prefix string) (name []string, link []string, err error)

	// Delete permanently removes all storage space associated
	// with a ref.
	Delete(ref string) error

	// Connect connects with the Google Cloud Platform.
	Connect()
}

// GCP is an implementation of Interface that connects to a live GCP instance.
type GCP struct {
	client          *http.Client
	service         *storage.Service
	projectId       string
	bucketName      string
	defaultWriteACL WriteACL
}

// Guarantee we implement the interface.
var _ Interface = (*GCP)(nil)

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

func (gcp *GCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// Insert an object into a bucket.
	object := &storage.Object{Name: ref}
	file, err := os.Open(srcLocalFilename)
	if err != nil {
		log.Printf("Error opening: %v", err)
		return "", err
	}
	defer file.Close()
	acl := string(gcp.defaultWriteACL)
	res, err := gcp.service.Objects.Insert(gcp.bucketName, object).Media(file).PredefinedAcl(acl).Do()
	if err == nil {
		log.Printf("Created object %v at location %v\n", res.Name, res.SelfLink)
	} else {
		log.Printf("Objects.Insert failed: %v", err)
	}
	return res.MediaLink, err
}

func (gcp *GCP) Get(ref string) (link string, error error) {
	// Get the link of the blob
	res, err := gcp.service.Objects.Get(gcp.bucketName, ref).Do()
	if err != nil {
		return "", err
	}
	fmt.Printf("The media download link for %v/%v is %v.\n\n", gcp.bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

func (gcp *GCP) Put(ref string, contents []byte) (refLink string, error error) {
	// TODO(edpin): this is not super safe, given the file has
	// permissions 0666. But for now the contents are always
	// public on cloud store, so it doesn't matter.
	f, err := ioutil.TempFile("", "upload-gcp-blob-")
	if err != nil {
		return "", err
	}
	defer f.Close()
	name := f.Name()
	defer os.Remove(name)
	n, err := f.Write(contents)
	if err != nil || n != len(contents) {
		return "", err
	}
	link, err := gcp.PutLocalFile(name, ref)

	return link, err
}

func (gcp *GCP) List(prefix string) (name []string, link []string, err error) {
	nextPageToken := ""
	for {
		moreNames, moreLinks, nextPageToken, err := gcp.innerList(prefix, nextPageToken)
		if err != nil {
			return nil, nil, err
		}
		name = append(name, moreNames...)
		link = append(link, moreLinks...)
		if nextPageToken == "" {
			break
		}
	}
	return
}

// innerList is an internal function that does what List does, except
// it accepts a continuation token and possibly returns one if there
// are more objects to retrieve.
func (gcp *GCP) innerList(prefix, pageToken string) (name []string, link []string, nextPageToken string, error error) {
	objs, err := gcp.service.Objects.List(gcp.bucketName).Prefix(prefix).Fields("items(name,mediaLink),nextPageToken").PageToken(pageToken).Do()
	if err != nil {
		return nil, nil, "", err
	}
	// objs.Items is a slice of Objects.

	// Allocate space for all returned objects in this call.
	name = make([]string, len(objs.Items))
	link = make([]string, len(objs.Items))

	for i, o := range objs.Items {
		name[i] = o.Name
		link[i] = o.MediaLink
	}

	return name, link, objs.NextPageToken, nil
}

func (gcp *GCP) Delete(ref string) error {
	err := gcp.service.Objects.Delete(gcp.bucketName, ref).Do()
	if err != nil {
		return err
	}
	return nil
}

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
