// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcs implements a storage backend that saves data to Google Cloud Storage.
package gcs // import "upspin.io/cloud/storage/gcs"

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	gContext "golang.org/x/net/context"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
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
	bucketName = "gcpBucketName"
	defaultACL = "defaultACL"
)

// gcsImpl is an implementation of Storage that connects to a Google Cloud Storage (GCS) backend.
type gcsImpl struct {
	client          *http.Client
	service         *gcsBE.Service
	bucketName      string
	defaultWriteACL string
}

// New initializes a Storage implementation that stores data to Google Cloud Storage.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/gcs.New"

	bucket, ok := opts.Opts[bucketName]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", bucketName))
	}
	acl, ok := opts.Opts[defaultACL]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", defaultACL))
	}

	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(gContext.Background(), scope)
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to get default client: %s", err))
	}
	service, err := gcsBE.New(client)
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to create storage service: %s", err))
	}

	return &gcsImpl{
		client:          client,
		service:         service,
		bucketName:      bucket,
		defaultWriteACL: acl,
	}, nil
}

func init() {
	storage.Register("GCS", New)
}

// Guarantee we implement the Storage interface.
var _ storage.Storage = (*gcsImpl)(nil)

// LinkBase implements Storage.
func (gcs *gcsImpl) LinkBase() (base string, err error) {
	return "https://storage.googleapis.com/" + gcs.bucketName + "/", nil
}

// Download implements Storage.
func (gcs *gcsImpl) Download(ref string) ([]byte, error) {
	const op = "cloud/storage/gcs.Download"
	resp, err := gcs.service.Objects.Get(gcs.bucketName, ref).Download()
	if err != nil {
		if gcsErr, ok := err.(*googleapi.Error); ok && gcsErr.Code == 404 {
			return nil, errors.E(op, errors.NotExist, err)
		}
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
func (gcs *gcsImpl) Put(ref string, contents []byte) error {
	const op = "cloud/storage/gcs.Put"
	buf := bytes.NewBuffer(contents)
	acl := string(gcs.defaultWriteACL)
	object := &gcsBE.Object{Name: ref}
	for tries := 0; ; tries++ {
		_, err := gcs.service.Objects.Insert(gcs.bucketName, object).Media(buf).PredefinedAcl(acl).Do()
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "503") || tries > 4 {
			return errors.E(op, errors.Transient, err)
		}
		log.Info.Printf("cloud/storage/gcs: WARNING: retrying Insert(%s): %s", ref, err)
		time.Sleep(time.Duration(100*(tries+1)) * time.Millisecond)
	}
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
	recordErr := func(err error) bool {
		if err == nil {
			return false
		}
		if firstErr == nil {
			firstErr = err
		}
		return true
	}
	for {
		objs, err := gcs.service.Objects.List(gcs.bucketName).MaxResults(maxParallelDeletes).Fields("items(name),nextPageToken").PageToken(pageToken).Do()
		if recordErr(err) {
			log.Error.Printf("EmptyBucket: List(%q): %v", gcs.bucketName, err)
			break
		}
		if verbose {
			log.Printf("Going to delete %d items from bucket %s", len(objs.Items), gcs.bucketName)
		}
		for _, o := range objs.Items {
			if verbose {
				log.Printf("Deleting: %q", o.Name)
			}
			err = gcs.Delete(o.Name)
			if recordErr(err) {
				log.Error.Printf("EmptyBucket: Delete(%q): %v", o.Name, err)
				continue
			}
		}
		if objs.NextPageToken == "" {
			break
		}
		pageToken = objs.NextPageToken
	}
	return firstErr
}

// Close implements Storage.
func (gcs *gcsImpl) Close() {
	// Not much to do, the GCS interface is pretty stateless (HTTP client).
	gcs.client = nil
	gcs.service = nil
}
