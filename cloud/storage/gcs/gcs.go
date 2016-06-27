// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcs implements a storage backend that saves data to Google Cloud Storage.
package gcs

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	gcsBE "google.golang.org/api/storage/v1"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
)

const (
	scope = gcsBE.DevstorageFullControlScope
)

// These constants define ACLs for writing data to Google Cloud Store.
// Definitions according to https://github.com/google/google-api-go-client/blob/master/storage/v1/storage-gen.go:
//   "publicReadWrite" - Project team owners get OWNER access, and
//       allUsers get WRITER access.
const (
	// PublicRead means project team owners get owner access and all users get reader access.
	PublicRead = "publicRead"
	// Private means project team owners get owner access.
	Private = "private"
	// ProjectPrivate means project team members get access according to their roles.
	ProjectPrivate = "projectPrivate"
	// BucketOwnerFullCtrl means the object owner gets owner access and project team owners get owner access.
	BucketOwnerFullCtrl = "bucketOwnerFullControl"
)

// Keys used for storing dial options.
const (
	projectID  = "gcpProjectID"
	bucketName = "gcpBucketName"
	defaultACL = "defaultACL"
)

// gcsImpl is an implementation of Storage that connects to a Google Cloud Storage (GCS) backend.
type gcsImpl struct {
	client          *http.Client
	service         *gcsBE.Service
	projectID       string
	bucketName      string
	defaultWriteACL string
}

// Guarantee we implement the Storage interface.
var _ storage.Storage = (*gcsImpl)(nil)

// PutLocalFile implements Storage.
func (gcs *gcsImpl) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// Insert an object into a bucket.
	object := &gcsBE.Object{Name: ref}
	file, err := os.Open(srcLocalFilename)
	if err != nil {
		log.Printf("Error opening: %v", err)
		return "", err
	}
	defer file.Close()
	acl := string(gcs.defaultWriteACL)
	res, err := gcs.service.Objects.Insert(gcs.bucketName, object).Media(file).PredefinedAcl(acl).Do()
	if err == nil {
		log.Debug.Printf("Created object %v at location %v", res.Name, res.SelfLink)
	} else {
		log.Error.Printf("Objects.Insert failed: %v", err)
		return "", err
	}
	return res.MediaLink, err
}

// Get implements Storage.
func (gcs *gcsImpl) Get(ref string) (link string, error error) {
	// Get the link of the blob
	res, err := gcs.service.Objects.Get(gcs.bucketName, ref).Do()
	if err != nil {
		return "", err
	}
	log.Debug.Printf("The media download link for %v/%v is %v.", gcs.bucketName, res.Name, res.MediaLink)
	return res.MediaLink, nil
}

// Download implements Storage.
func (gcs *gcsImpl) Download(ref string) ([]byte, error) {
	resp, err := gcs.service.Objects.Get(gcs.bucketName, ref).Download()
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

// Put implements Storage.
func (gcs *gcsImpl) Put(ref string, contents []byte) (refLink string, error error) {
	buf := bytes.NewBuffer(contents)
	acl := string(gcs.defaultWriteACL)
	object := &gcsBE.Object{Name: ref}
	res, err := gcs.service.Objects.Insert(gcs.bucketName, object).Media(buf).PredefinedAcl(acl).Do()
	if err == nil {
		log.Debug.Printf("Created object %v at location %v", res.Name, res.SelfLink)
	} else {
		log.Error.Printf("Objects.Insert failed: %v", err)
		return "", err
	}
	return res.MediaLink, err
}

// ListPrefilx implements Storage.
func (gcs *gcsImpl) ListPrefix(prefix string, depth int) ([]string, error) {
	var names []string
	pageToken := ""
	prefixDepth := strings.Count(prefix, "/")
	for {
		objs, err := gcs.service.Objects.List(gcs.bucketName).Prefix(prefix).Fields("items(name),nextPageToken").PageToken(pageToken).Do()
		if err != nil {
			return nil, err
		}
		innerNames := make([]string, 0, len(objs.Items))
		for _, o := range objs.Items {
			// Only append o.Name if it doesn't violate depth.
			objDepth := strings.Count(o.Name, "/")
			netDepth := objDepth - prefixDepth
			if netDepth < 0 {
				log.Error.Printf("WARN: Negative depth should never happen.")
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

// ListDir implements Storage.
func (gcs *gcsImpl) ListDir(dir string) ([]string, error) {
	var names []string
	pageToken := ""
	for {
		objs, err := gcs.service.Objects.List(gcs.bucketName).Prefix(dir).Delimiter("/").Fields("items(name),nextPageToken").PageToken(pageToken).Do()
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

// Delete implements Storage.
func (gcs *gcsImpl) Delete(ref string) error {
	err := gcs.service.Objects.Delete(gcs.bucketName, ref).Do()
	if err != nil {
		return err
	}
	return nil
}

// EmptyBucket completely removes all files in a bucket permanently.
// If verbose is true, every attempt to delete a file is logged to the standard logger.
// This is an expensive operation. It is also dangerous, so use with care.
// Exported, but not part of the GCP interface. Use for testing only.
func (gcs *gcsImpl) EmptyBucket(verbose bool) error {
	const maxParallelDeletes = 10
	pageToken := ""
	var firstErr error
	for {
		objs, err := gcs.service.Objects.List(gcs.bucketName).MaxResults(maxParallelDeletes).Fields("items(name),nextPageToken").PageToken(pageToken).Do()
		for _, o := range objs.Items {
			if verbose {
				log.Debug.Printf("Deleting: %q", o.Name)
			}
			err = gcs.Delete(o.Name)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				log.Debug.Printf("EmptyBucket: %q: %s", o.Name, err)
			}
		}
		if objs.NextPageToken == "" {
			break
		}
		pageToken = objs.NextPageToken
	}
	return firstErr
}

// Dial implements storage.Storage.
func (gcs *gcsImpl) Dial(opts *storage.StorageOpts) error {
	const Dial = "GCS.Dial"

	if v, ok := opts.Opts[projectID]; !ok {
		return errors.E(Dial, errors.Syntax, errors.Str("Project ID argument is required"))
	} else {
		gcs.projectID = v
	}
	if v, ok := opts.Opts[bucketName]; !ok {
		return errors.E(Dial, errors.Syntax, errors.Str("Bucket name argument is required"))
	} else {
		gcs.bucketName = v
	}
	if v, ok := opts.Opts[defaultACL]; !ok {
		gcs.defaultWriteACL = ProjectPrivate
	} else {
		gcs.defaultWriteACL = v
	}

	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(context.Background(), scope)
	if err != nil {
		return errors.E(Dial, errors.IO, errors.Errorf("Unable to get default client: %s", err))
	}
	service, err := gcsBE.New(client)
	if err != nil {
		errors.E(Dial, errors.IO, errors.Errorf("Unable to create storage service: %s", err))
	}
	// Initialize the object
	gcs.client = client
	gcs.service = service
	return nil
}

// Close implements Storage.
func (gcs *gcsImpl) Close() {
	// Not much to do, the GCS interface is pretty stateless (HTTP client).
	gcs.client = nil
	gcs.service = nil
}

func init() {
	storage.Register("GCS", &gcsImpl{})
}
