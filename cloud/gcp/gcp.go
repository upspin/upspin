// Package gcp implements a simple interface with the Google Cloud Platform
// for storing blobs in buckets and performing other types of maintenance on GCP.
package gcp

import (
	"bytes"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	storage "google.golang.org/api/storage/v1"
)

const (
	scope = storage.DevstorageFullControlScope
)

// WriteACL defines ACLs for writing data to Cloud Store.
// Definitions according to https://github.com/google/google-api-go-client/blob/master/storage/v1/storage-gen.go:
//   "publicReadWrite" - Project team owners get OWNER access, and
//       allUsers get WRITER access.
type WriteACL string

const (
	// PublicRead means project team owners get owner access and all users get reader access.
	PublicRead WriteACL = "publicRead"
	// Private means project team owners get owner access.
	Private WriteACL = "private"
	// ProjectPrivate means project team members get access according to their roles.
	ProjectPrivate WriteACL = "projectPrivate"
	// BucketOwnerFullCtrl means the object owner gets owner access and project team owners get owner access.
	BucketOwnerFullCtrl WriteACL = "bucketOwnerFullControl"
)

// GCP is how clients talk to GCP.
type GCP interface {
	// PutLocalFile copies a local file to GCP using ref as its
	// name. It returns a direct link for downloading the file
	// from GCP.
	PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error)

	// Get returns a link for downloading ref from GCP, if the ref
	// is publicly readable.
	Get(ref string) (link string, error error)

	// Download retrieves the bytes from the media link (even if
	// ref is not publicly readable).
	Download(ref string) ([]byte, error)

	// Put stores the contents given as ref on GCP.
	Put(ref string, contents []byte) (refLink string, error error)

	// ListPrefix lists all files that match a given prefix, up to a certain depth, counting from the prefix,
	// not absolute
	ListPrefix(prefix string, depth int) ([]string, error)

	// ListDir lists the contents of a given directory. It's equivalent to ListPrefix(dir, 1) but much more efficient.
	ListDir(dir string) ([]string, error)

	// Delete permanently removes all storage space associated
	// with a ref.
	Delete(ref string) error

	// Connect connects with the Google Cloud Platform.
	Connect()
}

// gcpImpl is an implementation of GCP that connects to a live GCP instance.
type gcpImpl struct {
	client          *http.Client
	service         *storage.Service
	projectID       string
	bucketName      string
	defaultWriteACL WriteACL
}

// Guarantee we implement the GCP interface.
var _ GCP = (*gcpImpl)(nil)

// New creates a new GCP instance associated with the given project id and bucket name.
func New(projectID, bucketName string, defaultWriteACL WriteACL) GCP {
	gcp := &gcpImpl{
		projectID:       projectID,
		bucketName:      bucketName,
		defaultWriteACL: defaultWriteACL,
	}
	gcp.Connect()
	return gcp
}

// PutLocalFile implements GCP.
func (gcp *gcpImpl) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
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
		log.Printf("Created object %v at location %v", res.Name, res.SelfLink)
	} else {
		log.Printf("Objects.Insert failed: %v", err)
		return "", err
	}
	return res.MediaLink, err
}

// Get implements GCP.
func (gcp *gcpImpl) Get(ref string) (link string, error error) {
	// Get the link of the blob
	res, err := gcp.service.Objects.Get(gcp.bucketName, ref).Do()
	if err != nil {
		return "", err
	}
	log.Printf("The media download link for %v/%v is %v.", gcp.bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

// Download implements GCP.
func (gcp *gcpImpl) Download(ref string) ([]byte, error) {
	resp, err := gcp.service.Objects.Get(gcp.bucketName, ref).Download()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// Put implements GCP.
func (gcp *gcpImpl) Put(ref string, contents []byte) (refLink string, error error) {
	buf := bytes.NewBuffer(contents)
	acl := string(gcp.defaultWriteACL)
	object := &storage.Object{Name: ref}
	res, err := gcp.service.Objects.Insert(gcp.bucketName, object).Media(buf).PredefinedAcl(acl).Do()
	if err == nil {
		log.Printf("Created object %v at location %v", res.Name, res.SelfLink)
	} else {
		log.Printf("Objects.Insert failed: %v", err)
		return "", err
	}
	return res.MediaLink, err
}

func (gcp *gcpImpl) ListPrefix(prefix string, depth int) ([]string, error) {
	var names []string
	pageToken := ""
	prefixDepth := strings.Count(prefix, "/")
	for {
		objs, err := gcp.service.Objects.List(gcp.bucketName).Prefix(prefix).Fields("items(name),nextPageToken").PageToken(pageToken).Do()
		if err != nil {
			return nil, err
		}
		innerNames := make([]string, 0, len(objs.Items))
		for _, o := range objs.Items {
			// Only append o.Name if it doesn't violate depth.
			objDepth := strings.Count(o.Name, "/")
			netDepth := objDepth - prefixDepth
			if netDepth < 0 {
				log.Printf("WARN: Negative depth should never happen.")
				continue
			}
			if netDepth <= depth {
				innerNames = append(innerNames, o.Name)
			}
		}
		names = append(names, innerNames...)
		if objs.NextPageToken == "" {
			break
		}
		pageToken = objs.NextPageToken
	}
	return names, nil
}

func (gcp *gcpImpl) ListDir(dir string) ([]string, error) {
	var names []string
	pageToken := ""
	for {
		objs, err := gcp.service.Objects.List(gcp.bucketName).Prefix(dir).Delimiter("/").Fields("items(name),nextPageToken").PageToken(pageToken).Do()
		if err != nil {
			return nil, err
		}
		innerNames := make([]string, len(objs.Items))
		for i, o := range objs.Items {
			innerNames[i] = o.Name
		}
		names = append(names, innerNames...)
		if objs.NextPageToken == "" {
			break
		}
		pageToken = objs.NextPageToken
	}
	return names, nil
}

// Delete implements GCP.
func (gcp *gcpImpl) Delete(ref string) error {
	err := gcp.service.Objects.Delete(gcp.bucketName, ref).Do()
	if err != nil {
		return err
	}
	return nil
}

// Connect implements GCP.
func (gcp *gcpImpl) Connect() {
	if gcp.projectID == "" {
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
