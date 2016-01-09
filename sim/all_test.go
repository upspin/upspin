package service

import (
	"fmt"
	"strings"
	"testing"
)

// Avoid networking for now.
type testAddrType struct{}

func (testAddrType) Network() string { return "test" }
func (testAddrType) String() string  { return "test:0.0.0.0" }

var testAddr testAddrType

const (
	user = "user@google.com"
	root = user + "/"
)

func TestMakeRootDirectory(t *testing.T) {
	t.Logf("test addr: %s\n", testAddr)
	ss := NewStorageService(Location{testAddr})
	ds := NewDirectoryService(ss)
	ref, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("Ref for root: %v\n", ref)
	// Fetch the directory back and inspect it.
	ciphertext, err := ss.Get(ref.Reference)
	if err != nil {
		t.Fatal("get directory:", err)
	}
	name, clear, err := unpackBlob(ciphertext)
	if err != nil {
		t.Fatal("unpack:", err)
	}
	t.Logf("%q: [% x]\n", name, clear)
	if name != root {
		t.Fatalf("get of root: should have name %q; has %q", root, name)
	}
	if len(clear) != 0 {
		t.Fatalf("get of root: non-empty payload")
	}
}

func TestPutTopLevelFile(t *testing.T) {
	ss := NewStorageService(Location{testAddr})
	ds := NewDirectoryService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	href, err := ds.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	// Fetch the data back and inspect it.
	ciphertext, err := ss.Get(href.Reference)
	if err != nil {
		t.Fatal("get blob:", err)
	}
	name, clear, err := unpackBlob(ciphertext)
	if err != nil {
		t.Fatal("unpack:", err)
	}
	t.Logf("%q: [% x]\n", name, clear)
	if name != fileName {
		t.Fatalf("get of %q has name %q", fileName, name)
	}
	str := string(clear)
	if str != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
	}
}

const nFile = 100

func TestPutHundredTopLevelFiles(t *testing.T) {
	ss := NewStorageService(Location{testAddr})
	ds := NewDirectoryService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]HintedReference, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := PathName(fmt.Sprintf("%s/file.%d", user, i))
		h, err := ds.Put(fileName, []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = h
	}
	// Read them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		ciphertext, err := ss.Get(href[j].Reference)
		if err != nil {
			t.Fatalf("%q: get blob: %v", fileName, err)
		}
		name, clear, err := unpackBlob(ciphertext)
		if err != nil {
			t.Fatal("unpack:", err)
		}
		t.Logf("%q: [% x]\n", name, clear)
		if name != fileName {
			t.Fatalf("get of %q has name %q", fileName, name)
		}
		str := string(clear)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}

func TestGetHundredTopLevelFiles(t *testing.T) {
	ss := NewStorageService(Location{testAddr})
	ds := NewDirectoryService(ss)
	_, err := ds.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	// Create a hundred files.
	href := make([]HintedReference, nFile)
	for i := 0; i < nFile; i++ {
		text := strings.Repeat(fmt.Sprint(i), i)
		fileName := PathName(fmt.Sprintf("%s/file.%d", user, i))
		h, err := ds.Put(fileName, []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
		href[i] = h
	}
	// Get them all back in funny order.
	for i := 0; i < nFile; i++ {
		j := 7 * i % nFile
		text := strings.Repeat(fmt.Sprint(j), j)
		fileName := PathName(fmt.Sprintf("%s/file.%d", user, j))
		// Fetch the data back and inspect it.
		h, data, err := ds.Get(fileName)
		if err != nil {
			t.Fatalf("%q: get file: %v", fileName, err)
		}
		if h != href[j] {
			t.Fatalf("%q: get file: bad hash")
		}
		str := string(data)
		if str != text {
			t.Fatalf("get of %q has text %q; should be %q", fileName, str, text)
		}
	}
}
