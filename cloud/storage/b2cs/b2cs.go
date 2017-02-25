// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package b2cs implements a storage backend that saves data to Backblaze B2 Cloud Storage.
package b2cs // import "upspin.io/cloud/storage/b2cs"

import (
	"bytes"
	"context"
	"io"

	b2api "github.com/kurin/blazer/b2"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// These constants define ACLs for writing data to B2 Cloud Storage
// Definitions according to https://www.backblaze.com/b2/docs/buckets.html
const (
	// Private means owner gets full access. No one else has access rights (default).
	Private = string(b2api.Private)
	// Public means owner gets full access, but everybody is allowed to
	// download the files in the bucket.
	Public = string(b2api.Public)
)

// Keys used for storing dial options.
const (
	bucketName = "b2csBucketName"
	defaultACL = "defaultACL"
)

// b2csImpl is an implementation of Storage that connects to B2 Cloud Storage
type b2csImpl struct {
	client *b2api.Client
	bucket *b2api.Bucket
}

// New initializes a Storage implementation that stores data to Google Cloud Storage.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/b2cs.New"

	bucketNameOpt, ok := opts.Opts[bucketName]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", bucketName))
	}
	acl, ok := opts.Opts[defaultACL]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", defaultACL))

	}
	if acl != Private && acl != Public {
		return nil, errors.E(op, errors.Invalid,
			errors.Errorf("valid ACL values for B2CS are %s and %s", Private, Public))
	}
	// TODO use credentials here
	client, err := b2api.NewClient(context.Background(), "foo", "bar")
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to create B2 session: %v", err))
	}
	bucket, err := client.Bucket(context.Background(), bucketNameOpt)
	if b2api.IsNotExist(err) {
		// TODO set bucketattrs with lifecycle rules that only keep the latest version of a file
		bucket, err = client.NewBucket(context.Background(), bucketNameOpt, nil)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to obtain B2 bucket reference: %v", err))
	}

	return &b2csImpl{
		client: client,
		bucket: bucket,
	}, nil
}

func init() {
	storage.Register("B2CS", New)
}

// Guarantee we implement the Storage interface.
var _ storage.Storage = (*b2csImpl)(nil)

// LinkBase implements Storage.
func (b2 *b2csImpl) LinkBase() (base string, err error) {
	const op = "cloud/storage/b2cs.LinkBase"
	if b2 == nil || b2.bucket == nil {
		return "", errors.E(op, errors.Transient, errors.Errorf("B2 implementation is not initialized"))
	}
	return b2.bucket.BaseURL(), nil
}

// Download implements Storage.
func (b2 *b2csImpl) Download(ref string) ([]byte, error) {
	const op = "cloud/storage/b2cs.Download"
	buf := &bytes.Buffer{}
	r := b2.bucket.Object(ref).NewReader(context.Background())
	defer r.Close()
	_, err := io.Copy(buf, r)
	if b2api.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to download ref %q from B2 bucket %q: %v", ref, b2.bucket.Name(), err))
	}

	return buf.Bytes(), nil
}

// Put implements Storage.
func (b2 *b2csImpl) Put(ref string, contents []byte) error {
	const op = "cloud/storage/b2cs.Put"
	buf := bytes.NewBuffer(contents)
	w := b2.bucket.Object(ref).NewWriter(context.Background())
	defer w.Close()
	_, err := io.Copy(w, buf)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("unable to upload ref %q to B1 bucket %q: %v", ref, b2.bucket.Name(), err))
	}
	return upspin.ErrNotSupported
}

// Delete implements Storage.
func (b2 *b2csImpl) Delete(ref string) error {
	const op = "cloud/storage/b2cs.Delete"
	err := b2.bucket.Object(ref).Delete(context.Background())
	if b2api.IsNotExist(err) {
		return errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("unable to delete ref %q from B2 bucket: %v", ref, b2.bucket.Name(), err))
	}
	return nil
}

// Close implements Storage.
func (b2 *b2csImpl) Close() {
	b2.bucket = nil
	b2.client = nil
}
