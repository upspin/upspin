package cache

import (
	"strings"
	"testing"
)

const (
	ref        = "1234"
	testString = "This is a test."
)

var (
	fc = NewFileCache("")
)

func TestPutAndGet(t *testing.T) {
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Errorf("Put returned error: %v", err)
	}
	r := fc.Get(ref)
	if r == nil {
		t.Fatalf("Can't get ref %v", ref)
	}
	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != len(testString) {
		t.Errorf("Error reading bytes from cache: n=%d", n)
	}
	if err != nil {
		t.Errorf("Error in read: %v", err)
	}
}

func TestRename(t *testing.T) {
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Errorf("Put returned error: %v", err)
	}
	newRef := "00000010101"
	fc.Rename(newRef, ref)
	r := fc.Get(newRef)
	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != len(testString) {
		t.Errorf("Error reading bytes from cache: n=%d", n)
	}
	if err != nil {
		t.Errorf("Error in read: %v", err)
	}
	// Old ref does not exist anymore
	r = fc.Get(ref)
	if r != nil {
		t.Errorf("Get got an old ref")
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

func TestPurge(t *testing.T) {
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Errorf("Put returned error: %v", err)
	}
	err = fc.Purge(ref)
	if err != nil {
		t.Errorf("Purge failed: %v", err)
	}
	err = fc.Purge(ref)
	if err == nil {
		t.Errorf("Purge failed to detect missing ref: %v", err)
	}
}

func TestIsCached(t *testing.T) {
	if fc.IsCached(ref) {
		t.Fatalf("Ref is already cached!")
	}
	err := fc.Put(ref, strings.NewReader(testString))
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if !fc.IsCached(ref) {
		t.Errorf("Ref was never cached")
	}
	fc.Purge(ref)
	if fc.IsCached(ref) {
		t.Errorf("Ref remains cached?")
	}
}

func TestMain(m *testing.M) {
	m.Run()
	fc.Delete()
}
