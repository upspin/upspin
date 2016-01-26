package testclient

import (
	"testing"

	"upspin.googlesource.com/upspin.git/directory/testdir"
	"upspin.googlesource.com/upspin.git/store/teststore"
	"upspin.googlesource.com/upspin.git/upspin"
)

// TODO: Copied from testdirectory/all_test.go. Make this publicly available.
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
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	client := New(testdir.NewService(ss), ss)
	loc, err := client.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("loc for root: %v\n", loc)
	// Fetch the directory back and inspect it.
	ciphertext, _, err := ss.Get(loc)
	if err != nil {
		t.Fatal("get directory:", err)
	}
	name, clear, err := teststore.UnpackBlob(ciphertext)
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

func TestPutGetTopLevelFile(t *testing.T) {
	ss := teststore.NewService(upspin.NetAddr{Addr: testAddr})
	client := New(testdir.NewService(ss), ss)
	_, err := client.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	_, err = client.Put(fileName, []byte(text), nil) // TODO: Metadata? Protocol?
	if err != nil {
		t.Fatal("put file:", err)
	}
	data, err := client.Get(fileName) // TODO: Metadata?
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
}
