package testclient

import (
	"bytes"
	"math/rand"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
)

// TODO: Copied from testdirectory/all_test.go. Make this publicly available.

type Setup struct {
	upspin.User
	upspin.Directory
	upspin.Store
}

func setup() (*Setup, error) {
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	us, err := access.BindUser(testContext, e)
	if err != nil {
		return nil, err
	}
	ds, err := access.BindDirectory(testContext, e)
	if err != nil {
		return nil, err
	}
	ss, err := access.BindStore(testContext, e)
	if err != nil {
		return nil, err
	}
	return &Setup{
		User:      us,
		Directory: ds,
		Store:     ss,
	}, nil
}

// TODO: End of copied code.

// installUser installs the named user into the User service with root directory in
// the Directory service. TODO: A bit of a hack.
func installUser(user upspin.User, dir upspin.Directory, userName upspin.UserName) {
	err := user.(*testuser.Service).Install(userName, dir)
	if err != nil {
		panic(err)
	}
}

func TestPutGetTopLevelFile(t *testing.T) {
	const (
		user = "user1@google.com"
		root = user + "/"
	)
	s, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	installUser(s.User, s.Directory, user)
	client := New(s.User, s.Store)
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
	Max = 100 * 1000 // Must be > 100.
)

func setupFileIO(user upspin.UserName, fileName upspin.PathName, t *testing.T) (*Client, upspin.File, []byte) {
	s, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	installUser(s.User, s.Directory, user)
	client := New(s.User, s.Store)
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
	const (
		user     = "user3@google.com"
		root     = user + "/"
		fileName = user + "/" + "file"
	)
	client, f, data := setupFileIO(user, fileName, t)

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
	const (
		user     = "user4@google.com"
		root     = user + "/"
		fileName = root + "file"
	)
	client, f, data := setupFileIO(user, fileName, t)

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
