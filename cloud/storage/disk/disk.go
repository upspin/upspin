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
	"upspin.io/log"
	"upspin.io/upspin"
)

// New initializes and returns a disk-backed storage.Storage with the given
// options. The single, required option is "basePath" that must be an absolute
// path under which all objects should be stored.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/disk.New"

	base, ok := opts.Opts["basePath"]
	if !ok {
		return nil, errors.E(op, errors.Str("the basePath option must be specified"))
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, errors.E(op, errors.IO, err)
	}

	if err := guaranteeNewEncoding(base); err != nil {
		return nil, errors.E(op, errors.IO, err)
	}

	return &storageImpl{base: base}, nil
}

// guaranteeNewEncoding makes sure we are using the new, safe path encoding.
// If we're not, it prints a recipe to update it and errors out.
func guaranteeNewEncoding(base string) error {
	// Make sure the disk tree is or will be using the new path encoding.
	// Three cases:
	// 1) Directory is empty. Use new encoding, and add "++" directory.
	// 2) Directory contains subdirectory "++". Use new encoding.
	// 3) Directory does non-empty and does not contain "++". Give error.

	// The "++" directory is used as an indicator that we are using the new
	// encoding. This might hold storage one day but will never exist if
	// using the old one, so it serves as a good marker.
	plusDir := filepath.Join(base, "++")
	if empty(base) {
		// New directory tree. Create the "++" directory as a marker.
		return os.MkdirAll(plusDir, 0700)
	}
	// Directory is not empty. It must contain "++".
	if _, err := os.Stat(plusDir); err != nil {
		log.Printf("Error starting server:\n")
		log.Printf("Base directory %q uses a deprecated path encoding.\n", base)
		log.Printf("It must be updated before serving again.\n")
		log.Printf("To update, choose a new location (not the existing one) and run:\n")
		log.Printf(`\tgo run upspin.io/cloud/storage/disk/convert.go -old=%q -new="<new-location>"\n`, base)
		log.Printf("Then restart server with option basePath=<new-location>\n")
		return errors.Errorf("base directory %q uses deprecated path encoding", base)
	}
	return nil
}

// empty reports whether the directory is non-existent or empty.
func empty(dir string) bool {
	fd, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true
		}
		log.Fatal(err)
	}
	defer fd.Close()
	names, err := fd.Readdirnames(0)
	if err != nil {
		log.Fatal(err)
	}
	return len(names) == 0
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
	return local.Path(s.base, ref)
}
