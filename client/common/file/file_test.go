package file

import (
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"
)

var _ upspin.Client = (*dummyClient)(nil)

func create(name upspin.PathName) upspin.File {
	return New(&dummyClient{}, true, name)
}

func open(name upspin.PathName, existingData []byte) upspin.File {
	f := New(&dummyClient{}, false, name)
	f.SetData(existingData)
	return f
}

func setupFileIO(fileName upspin.PathName, max int, t *testing.T) (upspin.File, []byte) {
	f := create(fileName)
	// Create a data set with each byte equal to its offset.
	data := make([]byte, max)
	for i := range data {
		data[i] = uint8(i)
	}
	return f, data
}

const (
	dummyData = "This is some dummy data."
)

var (
	fileName = upspin.PathName("foo@bar.com/hello.txt")
)

func TestWriteAndClose(t *testing.T) {
	f := create(fileName)
	n, err := f.Write([]byte(dummyData))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n != 24 {
		t.Errorf("Expected 24 bytes written, got %d", n)
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	realFile := f.(*F) // Get the real implementation
	dummyClient := realFile.Client().(*dummyClient)
	if string(dummyClient.putData) != dummyData {
		t.Errorf("Expected %s, got %s", dummyData, dummyClient.putData)
	}
}

func TestReadAndSeek(t *testing.T) {
	f := open(fileName, []byte(dummyData))
	buf := make([]byte, len(dummyData)+10)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n != len(dummyData) {
		t.Fatalf("Expected len %d, got %d", len(dummyData), n)
	}
	buf = buf[:n]
	if string(buf) != dummyData {
		t.Errorf("Expected %s, got %s", dummyData, buf)
	}
	// Now read at a random location
	n, err = f.ReadAt(buf, 8)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n != 16 {
		t.Fatalf("Expected 16, got %d", n)
	}
	buf = buf[:n]
	expectedSubString := "some dummy data."
	if string(buf) != expectedSubString {
		t.Errorf("Expected %s, got %s", expectedSubString, buf)
	}
	// Seek and read.
	n64, err := f.Seek(19, 0)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n64 != 19 {
		t.Fatalf("Expected 19, got %d", n)
	}
	n, err = f.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n != 5 {
		t.Errorf("Expected 5, got %d", n)
	}
	buf = buf[:n]
	expectedSubString = "data."
	if string(buf) != expectedSubString {
		t.Errorf("Expected %s, got %s", expectedSubString, buf)
	}
	// Seek to the middle and then seek some more from there.
	_, err = f.Seek(10, 0)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	n64, err = f.Seek(3, 1)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n64 != 13 {
		t.Fatalf("Expected 13, got %d", n)
	}
	buf = buf[0:30]
	log.Printf("buf=%s,len=%d", buf, len(buf))
	n, err = f.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	buf = buf[:n]
	expectedSubString = "dummy data."
	if string(buf) != expectedSubString {
		t.Errorf("Expected %s, got %s", expectedSubString, buf)
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
	// Write.
	f := create(fileName)
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
	f = create(fileName + "x")
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

var loc0 upspin.Location

type dummyClient struct {
	putData []byte
}

func (d *dummyClient) Get(name upspin.PathName) ([]byte, error) {
	return nil, nil
}
func (d *dummyClient) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	d.putData = make([]byte, len(data))
	copy(d.putData, data)
	return loc0, nil
}
func (d *dummyClient) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return loc0, nil
}
func (d *dummyClient) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) Create(name upspin.PathName) (upspin.File, error) {
	return nil, nil
}
func (d *dummyClient) Open(name upspin.PathName) (upspin.File, error) {
	return nil, nil
}
