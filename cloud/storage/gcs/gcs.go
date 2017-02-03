// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcs implements a storage backend that saves data to Google Cloud Storage.
package gcs

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

// Guarantee we implement the Storage interface.
var _ storage.Storage = (*gcsImpl)(nil)

// Get implements Storage.
func (gcs *gcsImpl) Get(ref string) (link string, err error) {
	const op = "cloud/storage/gcs.Get"
	// Get the link of the blob
	res, err := gcs.service.Objects.Get(gcs.bucketName, ref).Do()
	if err != nil {
		if gcsErr, ok := err.(*googleapi.Error); ok && gcsErr.Code == 404 {
			return "", errors.E(op, errors.NotExist, err)
		}
		return "", errors.E(op, err)
	}
	return res.MediaLink, nil
}

func (gcs *gcsImpl) LinkBase() (base string, err error) {
	const op = "cloud/storage/gcs.LinkBase"

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
func (gcs *gcsImpl) Put(ref string, contents []byte) (refLink string, error error) {
	const op = "cloud/storage/gcs.Put"
	buf := bytes.NewBuffer(contents)
	acl := string(gcs.defaultWriteACL)
	object := &gcsBE.Object{Name: ref}
	for tries := 0; ; tries++ {
		res, err := gcs.service.Objects.Insert(gcs.bucketName, object).Media(buf).PredefinedAcl(acl).Do()
		if err == nil {
			return res.MediaLink, err
		}
		if !strings.Contains(err.Error(), "503") || tries > 4 {
			return "", errors.E(op, errors.Transient, err)
		}
		log.Info.Printf("cloud/storage/gcs: WARNING: retrying Insert(%s): %s", ref, err)
		time.Sleep(time.Duration(100*(tries+1)) * time.Millisecond)
	}
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
		for _, o := range objs.Items {
			// Only append o.Name if it doesn't violate depth.
			objDepth := strings.Count(o.Name, "/")
			netDepth := objDepth - prefixDepth
			if netDepth < 0 {
				log.Error.Printf("cloud/storage/gcs: WARNING: negative depth should never happen.")
				continue
			}
			if netDepth <= depth {
				names = append(names, o.Name)
			}
		}
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
		for _, o := range objs.Items {
			names = append(names, o.Name)
		}
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

// Dial implements storage.Storage.
func (gcs *gcsImpl) Dial(opts *storage.Opts) error {
	const op = "cloud/storage/gcs.Dial"

	if v, ok := opts.Opts[bucketName]; ok {
		gcs.bucketName = v
	} else {
		return errors.E(op, errors.Invalid, errors.Str("Bucket name argument is required"))
	}
	if v, ok := opts.Opts[defaultACL]; ok {
		gcs.defaultWriteACL = v
	} else {
		return errors.E(op, errors.Invalid, errors.Str("Default ACL argument is required"))
	}

	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(gContext.Background(), scope)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("Unable to get default client: %s", err))
	}
	service, err := gcsBE.New(client)
	if err != nil {
		errors.E(op, errors.IO, errors.Errorf("Unable to create storage service: %s", err))
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
