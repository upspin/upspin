package gcp

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"upspin.io/cloud/gcp"
	"upspin.io/log"
	"upspin.io/upspin"
)

func createReadAndDelete(t *testing.T, wgStart *sync.WaitGroup, wgEnd *sync.WaitGroup, d *directory, path upspin.PathName, loopCount int, maxIdleMS int32) {
	wgEnd.Add(1)
	wgStart.Done()
	t.Log("Started Go routine for operating on %s", path)
	wgStart.Wait()

	var err error
	for i := 0; i < loopCount; i++ {
		de := &upspin.DirEntry{
			Name: path,
		}
		err = d.Put(de)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Lookup(path)
		if err != nil {
			t.Fatal(err)
		}
		err = d.Delete(path)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(rand.Int31n(maxIdleMS)) * time.Millisecond)
	}
	wgEnd.Done()
}

func TestParallelOperationsOnRoot(t *testing.T) {
	wgStart := new(sync.WaitGroup)
	wgEnd := new(sync.WaitGroup)

	d := startDir(t)

	wgStart.Add(3)

	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/a.txt"), 100, 10)
	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/b.txt"), 100, 10)
	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/c.txt"), 100, 10)

	wgStart.Wait()
	wgEnd.Wait()
}

func TestParallelOperationsOnCommonPath(t *testing.T) {
	wgStart := new(sync.WaitGroup)
	wgEnd := new(sync.WaitGroup)

	d := startDir(t)

	_, err := d.MakeDirectory(upspin.PathName(userName + "/a"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.MakeDirectory(upspin.PathName(userName + "/a/b"))
	if err != nil {
		t.Fatal(err)
	}

	wgStart.Add(4)

	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/a/b/f1.txt"), 100, 10)
	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/a/b/f2.txt"), 100, 10)
	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/a/f1.txt"), 100, 10)
	go createReadAndDelete(t, wgStart, wgEnd, d, upspin.PathName(userName+"/a/f2.txt"), 100, 10)

	wgStart.Wait()
	wgEnd.Wait()
}

func startDir(t *testing.T) *directory {
	log.SetLevel(log.Lerror) // silence most messages
	d := newTestDirServer(t, &gcpMock{storage: make(map[string][]byte)})

	root := &upspin.DirEntry{
		Name: userName,
		Metadata: upspin.Metadata{
			Attr: upspin.AttrDirectory,
		},
	}
	err := d.Put(root)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type gcpMock struct {
	mu      sync.Mutex
	storage map[string][]byte
}

var _ gcp.GCP = (*gcpMock)(nil)

// PutLocalFile implements gpc.GCP.
func (g *gcpMock) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	panic("not used")
}

// Get implements gpc.GCP.
func (g *gcpMock) Get(ref string) (link string, error error) {
	panic("not used")
}

// Download implements gpc.GCP.
func (g *gcpMock) Download(ref string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok := g.storage[ref]; ok {
		return v, nil
	}
	return nil, errors.New("404 not found")
}

// Put implements gcp.GCP.
func (g *gcpMock) Put(ref string, contents []byte) (refLink string, error error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.storage[ref] = contents
	return "", nil
}

// ListPrefix implements gcp.GCP.
func (g *gcpMock) ListPrefix(prefix string, depth int) ([]string, error) {
	panic("not used")
}

// ListDir implements gcp.GCP.
func (g *gcpMock) ListDir(dir string) ([]string, error) {
	panic("not used")
}

// Delete implements gcp.GCP.
func (g *gcpMock) Delete(ref string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.storage[ref]; ok {
		delete(g.storage, ref)
		return nil
	}
	return errors.New("404 not found")
}

// Connect implements gcp.GCP.
func (g *gcpMock) Connect() {
}
