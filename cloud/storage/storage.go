// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storage implements a low-level interface for storing blobs in
// stable storage such as a database.
package storage

// Storage is a low-level storage interface for services to store their data permanently.
type Storage interface {
	// PutLocalFile copies a local file to storage using ref as its
	// name. It may return a direct link for downloading the file
	// from the storage backend, or empty if the backend does not offer
	// direct links into it.
	PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error)

	// Get returns a link for downloading ref from the storage backend,
	// if the ref is publicly readable and the backend offers direct links.
	Get(ref string) (link string, error error)

	// Download retrieves the bytes associated with a ref.
	Download(ref string) ([]byte, error)

	// Put stores the contents given as ref on the storage backend.
	// It may return a direct link for retrieving data directly from
	// the backend, if it provides direct links.
	Put(ref string, contents []byte) (refLink string, error error)

	// ListPrefix lists all files that match a given prefix, up to a
	// certain depth, counting from the prefix, not absolute
	ListPrefix(prefix string, depth int) ([]string, error)

	// ListDir lists the contents of a given directory. It's equivalent to
	// ListPrefix(dir, 1) but much more efficient.
	ListDir(dir string) ([]string, error)

	// Delete permanently removes all storage space associated
	// with a ref.
	Delete(ref string) error

	// Connect connects with the storage backend. It must be called only once.
	Connect() error

	// Disconnect disconnects from the storage backend.
	// It must be called only once and only after Connect has succeeded.
	Disconnect()
}
