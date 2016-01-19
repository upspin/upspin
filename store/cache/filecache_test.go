// Tests for filecache
package cache

import (
	"strings"
	"testing"
)

var (
	fc         FileCache = FileCache{}
	ref        string    = "1234"
	testString string    = "This is a test."
)

func TestPutAndGet(t *testing.T) {
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Errorf("Filecache returned error: %v", err)
	}
	r := fc.Get(ref)
	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != len(testString) || err != nil {
		t.Errorf("Error reading from cache: n=%d, err: %v", n, err)
	}
}

func TestRename(t *testing.T) {
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Errorf("Filecache returned error: %q", err)
	}
	newRef := "00000010101"
	fc.Rename(newRef, ref)
	r := fc.Get(newRef)
	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != len(testString) || err != nil {
		t.Errorf("Error reading from cache: n=%d, err: %v", n, err)
	}
	// Old ref does not exist anymore
	r = fc.Get(ref)
	if r != nil {
		t.Errorf("Managed to get an old ref")
	}
}

func TestRenameFails(t *testing.T) {
	err := fc.Rename("newRef", "inexistent ref")
	if err == nil {
		t.Error("Failed to fail")
	}
}

func TestRandomRef(t *testing.T) {
	ref1 := fc.RandomRef()
	if ref1 == "" {
		t.Error("Got an empty ref1")
	}
	ref2 := fc.RandomRef()
	if ref2 == "" {
		t.Error("Got an empty ref2")
	}
	if ref1 == ref2 {
		t.Errorf("ref1 == ref2: %v", ref1)
	}
}
