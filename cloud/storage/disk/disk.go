// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package disk provides a storage.Storage that stores data on local disk.
package disk // import "upspin.io/cloud/storage/disk"

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/disk/internal/local"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// New initializes and returns a disk-backed storage.Storage with the given
// options. The single, required option is "basePath" that must be an absolute
// path under which all objects should be stored.
func New(opts *storage.Opts) (storage.Storage, error) {
	const op errors.Op = "cloud/storage/disk.New"

	base, ok := opts.Opts["basePath"]
	if !ok {
		return nil, errors.E(op, "the basePath option must be specified")
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
	// 3) Directory is non-empty and does not contain "++". Give error.

	// The "++" directory is used as an indicator that we are using the new
	// encoding. This might hold storage one day but will never exist if
	// using the old one, so it serves as a good marker.
	plusDir := filepath.Join(base, "++")
	empty, err := isEmpty(base)
	if err != nil {
		return err
	}
	if empty {
		// New directory tree. Create the "++" directory as a marker.
		return os.MkdirAll(plusDir, 0700)
	}
	// Directory is not empty. It must contain "++".
	if _, err := os.Stat(plusDir); err != nil {
		// Return a very long error explaining what to do.
		format := "Base directory %[1]q uses a deprecated path encoding.\n" +
			"It must be updated before serving again.\n" +
			"To update, move the tree aside to a backup location, and run:\n" +
			"\tgo run upspin.io/cloud/storage/disk/convert.go -old=<backup-location> -new=%[1]q\n" +
			"Then restart the server.\n"
		return errors.Errorf(format, base)
	}
	return nil
}

// isEmpty reports whether the directory is empty.
// The directory must exist; we have already created it if we needed to.
func isEmpty(dir string) (bool, error) {
	fd, err := os.Open(dir)
	if err != nil {
		return true, err
	}
	defer fd.Close()
	names, err := fd.Readdirnames(0)
	if err != nil {
		return true, err
	}
	return len(names) == 0, nil
}

func init() {
	storage.Register("Disk", New)
}

type storageImpl struct {
	base string
}

var (
	_ storage.Storage = (*storageImpl)(nil)
	_ storage.Lister  = (*storageImpl)(nil)
)

// LinkBase implements storage.Storage.
func (s *storageImpl) LinkBase() (base string, err error) {
	return "", upspin.ErrNotSupported
}

// Download implements storage.Storage.
func (s *storageImpl) Download(ref string) ([]byte, error) {
	const op errors.Op = "cloud/storage/disk.Download"
	b, err := ioutil.ReadFile(s.path(ref))
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, errors.Str(ref))
	} else if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	return b, nil
}

// Put implements storage.Storage.
func (s *storageImpl) Put(ref string, contents []byte) error {
	const op errors.Op = "cloud/storage/disk.Put"
	p := s.path(ref)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return errors.E(op, errors.IO, err)
	}
	if err := ioutil.WriteFile(p, contents, 0600); err != nil {
		return errors.E(op, errors.IO, err)
	}
	return nil
}

// Delete implements storage.Storage.
func (s *storageImpl) Delete(ref string) error {
	const op errors.Op = "cloud/storage/disk.Delete"
	if err := os.Remove(s.path(ref)); os.IsNotExist(err) {
		return errors.E(op, errors.NotExist, errors.Str(ref))
	} else if err != nil {
		return errors.E(op, errors.IO, err)
	}
	return nil
}

var maxRefsPerCall = 1000 // A variable so that it may be overridden by tests.

// List implements storage.Lister.
func (s *storageImpl) List(token string) (refs []upspin.ListRefsItem, next string, err error) {
	const op errors.Op = "cloud/storage/disk.List"
	err = filepath.Walk(s.base, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Convert path into its base path.
		path = strings.TrimPrefix(strings.TrimPrefix(path, s.base), string(filepath.Separator))

		// Ignore the root.
		if path == "" {
			return nil
		}

		// Stop walking when we've gathered enough refs.
		if len(refs) >= maxRefsPerCall {
			if next == "" {
				next = path
			}
			return filepath.SkipDir
		}

		// Don't process paths that come before our pagination token.
		if path < token {
			if fi.IsDir() && !strings.HasPrefix(token, path) {
				// Don't descend into irrelevant directories.
				return filepath.SkipDir
			}
			return nil
		}

		if fi.IsDir() {
			// Nothing more to do for directories.
			return nil
		}

		// Convert the file path into its reference name
		// and append it to refs.
		ref, err := local.Ref(path)
		if err != nil {
			return err
		}
		refs = append(refs, upspin.ListRefsItem{
			Ref:  upspin.Reference(ref),
			Size: fi.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, "", errors.E(op, err)
	}
	return refs, next, nil
}

// path returns the absolute path that should contain ref.
func (s *storageImpl) path(ref string) string {
	return local.Path(s.base, ref)
}
