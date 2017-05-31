// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package disk provides a storage.Storage that stores data on local disk.
package disk // import "upspin.io/cloud/storage/disk"

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/disk/internal/local"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// New initializes and returns a disk-backed storage.Storage with the given
// options. The single, required option is "basePath" that must be an absolute
// path under which all objects should be stored.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/disk.New"

	p, ok := opts.Opts["basePath"]
	if !ok {
		return nil, errors.E(op, errors.Str("the basePath option must be specified"))
	}
	if err := os.MkdirAll(p, 0700); err != nil {
		return nil, errors.E(op, errors.IO, err)
	}

	return &storageImpl{base: p}, nil
}

func init() {
	storage.Register("Disk", New)
}

type storageImpl struct {
	base string
}

var _ storage.Storage = (*storageImpl)(nil)

// LinkBase implements Storage.
func (s *storageImpl) LinkBase() (base string, err error) {
	return "", upspin.ErrNotSupported
}

// Download implements Storage.
func (s *storageImpl) Download(ref string) ([]byte, error) {
	const op = "cloud/storage/disk.Download"
	b, err := ioutil.ReadFile(s.path(ref))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, errors.Str(ref))
	} else if err != nil {
		return nil, errors.E(op, errors.IO, errors.Str(ref))
	}
	return b, nil
}

// Put implements Storage.
func (s *storageImpl) Put(ref string, contents []byte) error {
	const op = "cloud/storage/disk.Put"
	p := s.path(ref)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return errors.E(op, errors.IO, err)
	}
	if err := ioutil.WriteFile(p, contents, 0600); err != nil {
		return errors.E(op, errors.IO, err)
	}
	return nil
}

// Delete implements Storage.
func (s *storageImpl) Delete(ref string) error {
	const op = "cloud/storage/disk.Delete"
	if err := os.Remove(s.path(ref)); os.IsNotExist(err) {
		return errors.E(op, errors.NotExist, errors.Str(ref))
	} else if err != nil {
		return errors.E(op, errors.IO, errors.Str(ref))
	}
	return nil
}

// path returns the absolute path that should contain ref.
func (s *storageImpl) path(ref string) string {
	// TODO: Use local.Path once conversion tool is ready.
	return local.OldPath(s.base, ref)
}
