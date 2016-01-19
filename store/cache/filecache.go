// A file-based cache
package cache

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
)

var (
	tempDir   = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default")
	cacheRoot string
)

// Implements cache.Interface.
type FileCache struct {
}

func (fc FileCache) Put(ref string, blob io.Reader) error {
	f, err := createFile(ref)
	if err != nil {
		return err
	}
	io.Copy(f, blob)
	return nil
}

func (fc FileCache) Get(ref string) *bufio.Reader {
	f, err := fc.openForRead(ref)
	if err != nil {
		return nil
	}
	return bufio.NewReader(f)
}

func (fc FileCache) Rename(newRef, oldRef string) error {
	f, err := fc.openForRead(oldRef)
	if err != nil {
		return err
	}
	oldName := f.Name()
	f.Close()
	newF, err := createFile(newRef)
	if err != nil {
		return err
	}
	newName := newF.Name()
	newF.Close()
	return os.Rename(oldName, newName)
}

func (fc FileCache) RandomRef() string {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		panic("Can't create a tempfile")
	}
	defer f.Close()
	return f.Name()
}

func (fc FileCache) GetFileLocation(ref string) string {
	return fmt.Sprintf("%s/%s/blob", cacheRoot, ref)
}

func (fc FileCache) openForRead(ref string) (*os.File, error) {
	location := fc.GetFileLocation(ref)
	return os.Open(location)
}

func createFile(name string) (*os.File, error) {
	location := fmt.Sprintf("%s/%s", cacheRoot, name)
	if err := os.MkdirAll(location, 0755); err != nil {
		log.Fatalf("Can't MkdirAll %s: %q", location, err)
		return nil, err
	}
	dst := fmt.Sprintf("%s/blob", location)
	f, err := os.Create(dst)
	if err != nil {
		log.Fatalf("Can't create: %q", dst)
		return nil, err
	}
	return f, nil
}

func init() {
	flag.Parse()
	var err error
	cacheRoot, err = ioutil.TempDir(*tempDir, "upspin-cache-")
	if err != nil {
		log.Fatalf("Can't create tempdir: %q", err)
	}
}
