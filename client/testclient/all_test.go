package testclient

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
)

// TODO: Copied from testdirectory/all_test.go. Make this publicly available.

var context *upspin.Context

func setupContext() {
	if context != nil {
		return
	}

	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	// TODO: This bootstrapping is fragile and will break. It depends on the order of setup.
	context = new(upspin.Context)
	context.Packing = upspin.PlainPack // TODO.
	var err error
	context.User, err = access.BindUser(context, endpoint)
	if err != nil {
		panic(err)
	}
	context.Store, err = access.BindStore(context, endpoint)
	if err != nil {
		panic(err)
	}
	context.Directory, err = access.BindDirectory(context, endpoint)
	if err != nil {
		panic(err)
	}
}

func setup(userName upspin.UserName) {
	setupContext()
	context.UserName = userName
	err := context.User.(*testuser.Service).Install(userName, context.Directory)
	if err != nil {
		panic(err)
	}
}

// TODO: End of copied code.

func TestPutGetTopLevelFile(t *testing.T) {
	const (
		user = "user1@google.com"
		root = user + "/"
	)
	setup(user)
	client := New(context)
	const (
		fileName = root + "file"
		text     = "hello sailor"
	)
	_, err := client.Put(fileName, []byte(text)) // TODO: Packing?
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

func setupFileIO(user upspin.UserName, fileName upspin.PathName, max int, t *testing.T) (*Client, upspin.File, []byte) {
	setup(user)
	client := New(context)
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}

	// Create a data set with each byte equal to its offset.
	data := make([]byte, max)
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
	client, f, data := setupFileIO(user, fileName, Max, t)

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
	client, f, data := setupFileIO(user, fileName, Max, t)

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

func TestFileZeroFill(t *testing.T) {
	const (
		user     = "zerofill@google.com"
		root     = user + "/"
		fileName = user + "/" + "file"
	)
	client, f, _ := setupFileIO(user, fileName, 0, t)
	// Create and write one byte 100 bytes out.
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}
	const N = 100
	n64, err := f.Seek(N, 0)
	if err != nil {
		t.Fatal("seek file:", err)
	}
	if n64 != N {
		t.Fatalf("seek file: expected %d got %d", N, n64)
	}
	n, err := f.Write([]byte{'x'})
	if err != nil {
		t.Fatal("write file:", err)
	}
	if n != 1 {
		t.Fatalf("write file: expected %d got %d", 1, n)
	}
	f.Close()
	// Read it back.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal("open file:", err)
	}
	defer f.Close()
	buf := make([]byte, 2*N) // Much more than was written.
	// Make it all non-zero.
	for i := range buf {
		buf[i] = 'y'
	}
	n, err = f.Read(buf)
	if err != nil {
		t.Fatal("read file:", err)
	}
	if n != N+1 {
		t.Fatalf("read file: expected %d got %d", N+1, n)
	}
	for i := 0; i < N; i++ {
		if buf[i] != 0 {
			t.Errorf("byte %d should be 0 is %#.2x", i, buf[i])
		}
	}
	if buf[N] != 'x' {
		t.Errorf("byte %d should be 'x' is %#.2x", N, buf[N])
	}
}

func TestFileOverflow(t *testing.T) {
	maxInt = 100
	defer func() { maxInt = int64(^uint(0) >> 1) }()
	const (
		user     = "overflow@google.com"
		root     = user + "/"
		fileName = user + "/" + "file"
	)
	client, f, _ := setupFileIO(user, fileName, 0, t)
	// Write.
	f, err := client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}
	defer f.Close()
	buf := make([]byte, maxInt)
	n, err := f.Write(buf)
	if err != nil {
		t.Fatal("write file:", err)
	}
	if n != int(maxInt) {
		t.Fatalf("write file: expected %d got %d", maxInt, n)
	}
	n, err = f.Write(make([]byte, maxInt))
	if err == nil {
		t.Fatal("write file: expected overflow")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Fatal("write file: expected overflow error, got", err)
	}
	// Seek.
	n64, err := f.Seek(0, 0)
	if err != nil {
		t.Fatal("seek file:", err)
	}
	if n64 != 0 {
		t.Fatalf("seek begin file: expected 0 got %d", n64)
	}
	n64, err = f.Seek(maxInt, 0)
	if err != nil {
		t.Fatal("seek end file:", err)
	}
	if n64 != maxInt {
		t.Fatalf("seek file: expected %d got %d", maxInt, n64)
	}
	n64, err = f.Seek(maxInt+1, 0)
	if err == nil {
		t.Fatal("seek past file: expected error")
	}
	// One more trick: Create empty file, then check seek.
	f, err = client.Create(fileName + "x")
	if err != nil {
		t.Fatal("create filex:", err)
	}
	defer f.Close()
	n64, err = f.Seek(maxInt, 0)
	if err != nil {
		t.Fatal("seek maxInt filex:", err)
	}
	if n64 != maxInt {
		t.Fatalf("seek filex: expected %d got %d", maxInt, n64)
	}
	n64, err = f.Seek(maxInt+1, 0)
	if err == nil {
		t.Fatal("seek maxint+1 filex: expected error")
	}
}

func TestGlob(t *testing.T) {
	const user = "multiuser@a.co"
	setup(user)
	client := New(context)
	var err error
	var paths []*upspin.DirEntry
	writeFile := func(fileNumber int) {
		fileName := fmt.Sprintf("%s/testfile%d.txt", user, fileNumber)
		text := fmt.Sprintf("Contents of file %s", fileName)
		_, err = client.Put(upspin.PathName(fileName), []byte(text))
		if err != nil {
			t.Fatal("put file:", err)
		}
	}
	checkPaths := func(expPaths ...string) {
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if len(paths) != len(expPaths) {
			t.Fatalf("Expected %d paths, got %d", len(expPaths), len(paths))
		}
		for i, p := range expPaths {
			if string(paths[i].Name) != p {
				t.Errorf("Expected path %d %q, got %q", i, p, paths[i])
			}
		}
	}

	writeFile(0)
	writeFile(1)
	writeFile(7)
	writeFile(17)

	paths, err = client.Glob("multiuser@a.co/testfile*.txt")
	checkPaths("multiuser@a.co/testfile0.txt", "multiuser@a.co/testfile1.txt", "multiuser@a.co/testfile17.txt", "multiuser@a.co/testfile7.txt")

	paths, err = client.Glob("multiuser@a.co/*7.txt")
	checkPaths("multiuser@a.co/testfile17.txt", "multiuser@a.co/testfile7.txt")

	paths, err = client.Glob("multiuser@a.co/*1*.txt")
	checkPaths("multiuser@a.co/testfile1.txt", "multiuser@a.co/testfile17.txt")
}
