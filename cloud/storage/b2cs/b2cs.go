// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package b2cs implements a storage backend that saves data to Backblaze B2 Cloud Storage.
package b2cs // import "upspin.io/cloud/storage/b2cs"

import (
	"bytes"
	"context"
	"fmt"
	"io"

	b2api "github.com/kurin/blazer/b2"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Keys used for storing dial options.
const (
	accountID  = "b2csAccount"
	appKey     = "b2csAppKey"
	bucketName = "b2csBucketName"
)

// b2csImpl is an implementation of Storage that connects to B2 Cloud Storage
type b2csImpl struct {
	client *b2api.Client
	bucket *b2api.Bucket
	access b2api.BucketType
}

// New initializes a Storage implementation that stores data to B2 Cloud Storage.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/b2cs.New"

	accountIDOpt, ok := opts.Opts[accountID]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", accountID))

	}
	appKeyOpt, ok := opts.Opts[appKey]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", appKey))

	}
	bucketNameOpt, ok := opts.Opts[bucketName]
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("%q option is required", bucketName))
	}

	client, err := b2api.NewClient(context.Background(), accountIDOpt, appKeyOpt)
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to create B2 session: %v", err))
	}
	bucket, err := client.Bucket(context.Background(), bucketNameOpt)
	if b2api.IsNotExist(err) {
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
	if b2.access == "" {
		b2.checkAccess()
	}
	switch b2.access {
	default:
		return "", upspin.ErrNotSupported
	case b2api.Public:
		return fmt.Sprintf("%s/file/%s/", b2.bucket.BaseURL(), b2.bucket.Name()), nil
	}
}

// Download implements Storage.
func (b2 *b2csImpl) Download(ref string) ([]byte, error) {
	const op = "cloud/storage/b2cs.Download"
	buf := &bytes.Buffer{}
	r := b2.bucket.Object(ref).NewReader(context.Background())
	_, err := io.Copy(buf, r)
	if b2api.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to download ref %q from B2 bucket %q: %v", ref, b2.bucket.Name(), err))
	}
	err = r.Close()
	if err != nil {
		return nil, errors.E(op, errors.IO, errors.Errorf("unable to finish download of ref %q from B2 bucket %q: %v", ref, b2.bucket.Name(), err))
	}

	return buf.Bytes(), nil
}

// Put implements Storage.
func (b2 *b2csImpl) Put(ref string, contents []byte) error {
	const op = "cloud/storage/b2cs.Put"
	buf := bytes.NewBuffer(contents)
	w := b2.bucket.Object(ref).NewWriter(context.Background())
	_, err := io.Copy(w, buf)
	if err != nil {
		_ = w.Close()
		return errors.E(op, errors.IO, errors.Errorf("unable to upload ref %q to B1 bucket %q: %v", ref, b2.bucket.Name(), err))
	}
	err = w.Close()
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("unable to finish upload of ref %q to B1 bucket %q: %v", ref, b2.bucket.Name(), err))
	}
	return nil
}

// Delete implements Storage.
func (b2 *b2csImpl) Delete(ref string) error {
	const op = "cloud/storage/b2cs.Delete"
	o := b2.bucket.Object(ref)
	err := o.Delete(context.Background())
	if b2api.IsNotExist(err) {
		return errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("unable to delete ref %q from B2 bucket %q: %v", ref, b2.bucket.Name(), err))
	}
	return nil
}

// Close implements Storage.
func (b2 *b2csImpl) Close() {
	b2.bucket = nil
	b2.client = nil
}

func (b2 *b2csImpl) deleteBucket() error {
	var (
		c       *b2api.Cursor
		listErr error
	)
	// Remove all content from the bucket first,
	// otherwise the deletion will fail.
	for listErr != io.EOF {
		var objs []*b2api.Object
		objs, c, listErr = b2.bucket.ListObjects(context.Background(), 128, c)
		if listErr != nil && listErr != io.EOF {
			return listErr
		}
		for i := range objs {
			if err := objs[i].Delete(context.Background()); err != nil {
				return err
			}
		}
	}
	return b2.bucket.Delete(context.Background())
}

// checkAccess retrieves b2.attrs as the attributes from b2.bucket or sets a useful fallback value.
func (b2 *b2csImpl) checkAccess() {
	if b2 == nil || b2.bucket == nil {
		return
	}
	b2.access = b2api.Private
	attrs, err := b2.bucket.Attrs(context.Background())
	if err != nil {
		// Use the fallback, that's all the error handling we need.
		return
	}
	b2.access = attrs.Type
}
