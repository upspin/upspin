package testclient

import (
	"bytes"
	"math/rand"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/directory/testdir"
	"upspin.googlesource.com/upspin.git/store/teststore"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/testuser"
)

// TODO: Copied from testdirectory/all_test.go. Make this publicly available.

// Avoid networking for now.
const testAddr = "test:0.0.0.0"

type Context string

func (c Context) Name() string {
	return string(c)
}

var _ upspin.ClientContext = (*Context)(nil)

type Setup struct {
	upspin.User
	upspin.Store
	upspin.Directory
}

func setup() (*Setup, error) {
	ctxt := Context("testcontext")
	e := upspin.Endpoint{
		Transport: "in-process",
		NetAddr:   testAddr,
	}
	us, err := access.Switch.BindUser(ctxt, e)
	if err != nil {
		return nil, err
	}
	ds, err := access.Switch.BindDirectory(ctxt, e)
	if err != nil {
		return nil, err
	}
	// HACK: We set the store for the blobs to be the same as for the directory.
	return &Setup{
		User:      us,
		Store:     ds.(*testdir.Service).Store,
		Directory: ds,
	}, nil
}

// TODO: End of copied code.

const (
	user = "user@google.com"
	root = user + "/"
)

func TestMakeRootDirectory(t *testing.T) {
	s, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	client := New(s.Directory, s.Store)
	loc, err := client.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	t.Logf("loc for root: %v\n", loc)
	// Fetch the directory back and inspect it.
	ciphertext, _, err := s.Store.Get(loc)
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
	s, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	client := New(s.Directory, s.Store)
	_, err = client.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	_, err = client.Put(fileName, []byte(text)) // TODO: Packing?
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

const (
	Max      = 100 * 1000 // Must be > 100.
	fileName = root + "file"
)

func setupFileIO(t *testing.T) (*Client, upspin.File, []byte) {
	s, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	client := New(s.Directory, s.Store)
	_, err = client.MakeDirectory(user)
	if err != nil {
		t.Fatal("make directory:", err)
	}
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}

	// Create a data set with each byte equal to its offset.
	data := make([]byte, Max)
	for i := range data {
		data[i] = uint8(i)
	}
	return client, f, data
}

func TestFileSequentialAccess(t *testing.T) {
	client, f, data := setupFileIO(t)

	// Write the file in randomly sized chunks until it's full.
	for offset, length := 0, 0; offset < Max; offset += length {
		// Pick a random length.
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Write(data[offset : offset+length])
		if err != nil {
			t.Fatalf("Write(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Write length failed: offset %d expected %d got %d", offset, length, n)
		}
	}
	err := f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Now read it back with a similar scan.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, Max)
	for offset, length := 0, 0; offset < Max; offset += length {
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Read(buf[offset : offset+length])
		if err != nil {
			t.Fatalf("Read(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Read length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			if buf[i] != data[i] {
				t.Fatalf("Read at %d (%#x): expected %#.2x got %#.2x", i, i, data[i], buf[i])
			}
		}
	}
}

func TestFileRandomAccess(t *testing.T) {
	client, f, data := setupFileIO(t)

	// Use WriteAt at random offsets and random sizes to create file.
	// Start with a map of bools (easy) saying the byte has been written.
	// Loop until its length is the file size, meaning every byte has been written.
	written := make(map[int]bool)
	for len(written) != Max {
		// Pick a random offset and length.
		offset := rand.Intn(Max)
		// Don't bother starting at a known location - speeds up the coverage.
		for written[offset] {
			offset = rand.Intn(Max)
		}
		length := rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.WriteAt(data[offset:offset+length], int64(offset))
		if err != nil {
			t.Fatalf("WriteAt(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("WriteAt length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			written[i] = true
		}
	}
	err := f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Read file back all at once, for simple verification.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := make([]byte, Max)
	n, err := f.Read(result)
	if err != nil {
		t.Fatal(err)
	}
	if n != Max {
		t.Fatalf("Read: expected %d got %d", Max, n)
	}
	if !bytes.Equal(data, result) {
		for i, c := range data {
			if result[i] != c {
				t.Fatalf("byte at offset %d should be %#.2x is %#.2x", i, c, result[i])
			}
		}
	}

	// Now use a similar algorithm to WriteAt but with ReadAt to check random access.
	read := make(map[int]bool)
	buf := make([]byte, Max)
	for len(read) != Max {
		// Pick a random offset and length.
		offset := rand.Intn(Max)
		// Don't bother starting at a known location - speeds up the coverage.
		for read[offset] {
			offset = rand.Intn(Max)
		}
		length := rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.ReadAt(buf[offset:offset+length], int64(offset))
		if err != nil {
			t.Fatalf("ReadAt(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("ReadAt length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			if buf[i] != data[i] {
				t.Fatalf("ReadAt at %d (%#x): expected %#.2x got %#.2x", i, i, data[i], buf[i])
			}
		}
		for i := offset; i < offset+length; i++ {
			read[i] = true
		}
	}
}
