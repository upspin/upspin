// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package disk provides a storage.Storage that stores data on local disk.
package disk // import "upspin.io/cloud/storage/disk"

import (
	"encoding/base64"
	"io/ioutil"
	"os"
	"path/filepath"

	"upspin.io/cloud/storage"
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

// path returns the absolute file path to hold the contents of the blob
// with the specified reference.
func (s *storageImpl) path(ref string) string {
	// The provided reference may not be safe so base64-encode it.
	// We divide the structure into subdirectories with two-byte names
	// to an arbitrary depth.  We must be careful though not to create
	// a directory the same name as a blob. This is easily done by
	// making all blob names an odd number of bytes long. The
	// base64 URL character set includes only - and _ as punctuation,
	// so we use + as a pad. We also pad to make sure the path is long
	// enough.
	// We avoid multiple allocations by thinking ahead.
	enc := base64.RawURLEncoding
	length := enc.EncodedLen(len(ref))
	buf := make([]byte, length, length+4) // Extra room to pad.
	enc.Encode(buf, []byte(ref))
	const (
		numDirs  = 5
		numElems = 1 + numDirs + 1 // base plus up to 5 directories plus tail.
	)
	// Need at least 3 bytes, two for a directory and one for a file.
	// An empty ref gets the blob name "..../++/+".
	for len(buf) < 3 {
		buf = append(buf, '+')
	}
	// Blob needs a padding byte if it's short and could be confused with a directory.
	if len(buf) < 2*(numDirs+1)+1 && len(buf)%2 == 0 {
		buf = append(buf, '+')
	}
	str := string(buf) // This is the blob name to turn into a file path name.
	elems := make([]string, 0, numElems)
	elems = append(elems, s.base)
	for i := 1; i < numElems-1; i++ {
		if len(str) < 3 {
			break
		}
		elems = append(elems, str[:2])
		str = str[2:]
	}
	elems = append(elems, str)
	return filepath.Join(elems...)
}
