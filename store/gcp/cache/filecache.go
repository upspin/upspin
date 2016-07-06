// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/errors"
)

// FileCache implements a StoreCache for storing local files.
type FileCache struct {
	m         sync.Mutex // protects the root
	cacheRoot string
}

// Put implements StoreCache.
func (fc *FileCache) Put(ref string, blob io.Reader) error {
	f, err := fc.createFile(ref)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, blob)
	return err
}

// Get implements StoreCache.
func (fc *FileCache) Get(ref string) *bufio.Reader {
	f, err := fc.OpenRefForRead(ref)
	if err != nil {
		return nil
	}
	return bufio.NewReader(f)
}

// Rename implements StoreCache.
func (fc *FileCache) Rename(newRef, oldRef string) error {
	f, err := fc.OpenRefForRead(oldRef)
	if err != nil {
		return err
	}
	defer f.Close()
	oldName := f.Name()
	newF, err := fc.createFile(newRef)
	if err != nil {
		return err
	}
	defer newF.Close()
	newName := newF.Name()
	return os.Rename(oldName, newName)
}

// RandomRef implements StoreCache.
func (fc *FileCache) RandomRef() string {
	fc.m.Lock()
	defer fc.m.Unlock()
	f, err := ioutil.TempFile(fc.cacheRoot, "upload-")
	if err != nil {
		panic("Can't create a tempfile")
	}
	defer f.Close()
	_, fname := filepath.Split(f.Name())
	return fname
}

// Purge implements StoreCache.
func (fc *FileCache) Purge(ref string) error {
	return os.Remove(fc.GetFileLocation(ref))
}

// IsCached implements StoreCache.
func (fc *FileCache) IsCached(ref string) bool {
	fname := fc.GetFileLocation(ref)
	fi, err := os.Stat(fname)
	return err == nil && fi.Mode().IsRegular()
}

// GetFileLocation returns the full pathname of the location of the file.
func (fc *FileCache) GetFileLocation(ref string) string {
	fc.m.Lock()
	defer fc.m.Unlock()
	return fmt.Sprintf("%s/%s", fc.cacheRoot, ref)
}

// OpenRefForRead opens a given ref stored in the cache for read-only.
func (fc *FileCache) OpenRefForRead(ref string) (*os.File, error) {
	location := fc.GetFileLocation(ref)
	return os.Open(location)
}

func (fc *FileCache) createFile(name string) (*os.File, error) {
	location := fc.GetFileLocation(name)
	log.Printf("Creating file %v\n", location)
	f, err := os.Create(location)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	return f, nil
}

// Root returns the root of the cache.
func (fc *FileCache) Root() string {
	return fc.cacheRoot
}

// NewFileCache creates a new FileCache rooted under cacheRootDir.
// The directory must exist and be writable. If it doesn't exist, an attempt is
// made to create it.
func NewFileCache(cacheRootDir string) (*FileCache, error) {
	if cacheRootDir == "" {
		return nil, errors.E(NewFileCache, errors.Syntax, errors.Str("cacheRootDir can't be empty"))
	}
	err := os.MkdirAll(cacheRootDir, os.ModeDir|0755)
	if err != nil {
		return nil, errors.E("NewFileCache", errors.IO, err)
	}
	fc := &FileCache{cacheRoot: cacheRootDir}
	return fc, nil
}

// Delete removes all files from the cache and invalidates
// it. Further calls to any FileCache methods may fail unpredictably
// or silently.
func (fc *FileCache) Delete() {
	fc.m.Lock()
	defer fc.m.Unlock()
	err := os.RemoveAll(fc.cacheRoot)
	if err != nil {
		log.Fatalf("Can't delete cache dir: %v", err)
	}
	fc.cacheRoot = "/dev/null"
}
