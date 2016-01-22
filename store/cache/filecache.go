package cache

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

// FileCache implements a cache.Interface for storing local files.
type FileCache struct {
	cacheRoot string
}

func (fc FileCache) Put(ref string, blob io.Reader) error {
	f, err := fc.createFile(ref)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, blob)
	return err
}

func (fc FileCache) Get(ref string) *bufio.Reader {
	f, err := fc.OpenRefForRead(ref)
	if err != nil {
		return nil
	}
	return bufio.NewReader(f)
}

func (fc FileCache) Rename(newRef, oldRef string) error {
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

func (fc FileCache) RandomRef() string {
	f, err := ioutil.TempFile(fc.cacheRoot, "upload-")
	if err != nil {
		panic("Can't create a tempfile")
	}
	defer f.Close()
	_, fname := filepath.Split(f.Name())
	return fname
}

func (fc FileCache) Purge(ref string) error {
	return os.Remove(fc.GetFileLocation(ref))
}

func (fc FileCache) IsCached(ref string) bool {
	fname := fc.GetFileLocation(ref)
	fi, err := os.Stat(fname)
	return err == nil && fi.Mode().IsRegular()
}

func (fc FileCache) GetFileLocation(ref string) string {
	return fmt.Sprintf("%s/%s", fc.cacheRoot, ref)
}

func (fc FileCache) OpenRefForRead(ref string) (*os.File, error) {
	location := fc.GetFileLocation(ref)
	return os.Open(location)
}

func (fc FileCache) createFile(name string) (*os.File, error) {
	location := fmt.Sprintf("%s/%s", fc.cacheRoot, name)
	log.Printf("Creating file %v\n", location)
	f, err := os.Create(location)
	if err != nil {
		log.Fatalf("Can't create: %q", location)
		return nil, err
	}
	return f, nil
}

// NewFileCache creates a new FileCache rooted under cacheRootDir, if
// that dir is available. If it's not available, it returns nil. An
// empty argument uses the system's default location (but it's not guaranteed to succeed).
func NewFileCache(cacheRootDir string) *FileCache {
	cacheRoot, err := ioutil.TempDir(cacheRootDir, "upspin-cache-")
	if err != nil {
		log.Fatalf("Can't create tempdir: %v", err)
	}
	fc := &FileCache{cacheRoot}
	return fc
}
