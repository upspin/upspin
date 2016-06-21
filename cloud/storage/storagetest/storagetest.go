// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storagetest implements simple types and utility functions to help test
// implementations of storage.S.
package storagetest

import (
	"errors"

	"upspin.io/cloud/storage"
)

// DummyStorage is a dummy version of storage.S that does nothing.
type DummyStorage struct {
}

var _ storage.S = (*DummyStorage)(nil)

// PutLocalFile implements storage.S.
func (m *DummyStorage) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", nil
}

// Get implements storage.S.
func (m *DummyStorage) Get(ref string) (link string, error error) {
	return "", nil
}

// Download implements storage.S.
func (m *DummyStorage) Download(ref string) ([]byte, error) {
	return nil, nil
}

// Put implements storage.S.
func (m *DummyStorage) Put(ref string, contents []byte) (refLink string, error error) {
	return "", nil
}

// ListPrefix implements storage.S.
func (m *DummyStorage) ListPrefix(prefix string, depth int) ([]string, error) {
	return []string{}, nil
}

// ListDir implements storage.S.
func (m *DummyStorage) ListDir(dir string) ([]string, error) {
	return []string{}, nil
}

// Delete implements storage.S.
func (m *DummyStorage) Delete(ref string) error {
	return nil
}

// Connect implements storage.S.
func (m *DummyStorage) Connect() {
}

// ExpectGet is a DummyStorage that expects Get will be called with a
// given ref and when it does, it replies with the preset link.
type ExpectGet struct {
	DummyStorage
	Ref  string
	Link string
}

// Get implements storage.S.
func (e *ExpectGet) Get(ref string) (link string, error error) {
	if ref == e.Ref {
		return e.Link, nil
	}
	return "", errors.New("not found")
}

// ExpectDownloadCapturePut inspects all calls to Download with the
// given Ref and if it matches, it returns Data. Ref matches are strictly sequential.
// It also captures all Put requests.
type ExpectDownloadCapturePut struct {
	DummyStorage
	// Expectations for calls to Download
	Ref  []string
	Data [][]byte
	// Storage for calls to Put
	PutRef      []string
	PutContents [][]byte

	pos int // position of the next Ref to match
}

// Download implements storage.S.
func (e *ExpectDownloadCapturePut) Download(ref string) ([]byte, error) {
	if e.pos < len(e.Ref) && ref == e.Ref[e.pos] {
		data := e.Data[e.pos]
		e.pos++
		return data, nil
	}
	return nil, errors.New("not found")
}

// Put implements storage.S.
func (e *ExpectDownloadCapturePut) Put(ref string, contents []byte) (refLink string, error error) {
	e.PutRef = append(e.PutRef, ref)
	e.PutContents = append(e.PutContents, contents)
	return "", nil
}
