// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"strings"
	"testing"

	"upspin.io/upspin"
)

func create(name upspin.PathName) upspin.File {
	return Writable(&dummyClient{}, name)
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
	if n != len(dummyData) {
		t.Errorf("Expected %d bytes written, got %d", len(dummyData), n)
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	realFile := f.(*File) // Get the real implementation
	dummyClient := realFile.client.(*dummyClient)
	if string(dummyClient.putData) != dummyData {
		t.Errorf("Expected %s, got %s", dummyData, dummyClient.putData)
	}
}

func TestFileOverflow(t *testing.T) {
	maxInt = 100
	defer func() { maxInt = int64(^uint(0) >> 1) }()
	const (
		user     = "overflow@google.com"
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
	_, err = f.Write(make([]byte, maxInt))
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
	_, err = f.Seek(maxInt+1, 0)
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
	_, err = f.Seek(maxInt+1, 0)
	if err == nil {
		t.Fatal("seek maxint+1 filex: expected error")
	}
}

type dummyClient struct {
	putData []byte
}

var _ upspin.Client = (*dummyClient)(nil)

func (d *dummyClient) Get(name upspin.PathName) ([]byte, error) {
	return nil, nil
}
func (d *dummyClient) Lookup(name upspin.PathName, followFinal bool) (*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) Put(name upspin.PathName, data []byte) (*upspin.DirEntry, error) {
	d.putData = make([]byte, len(data))
	copy(d.putData, data)
	return nil, nil
}
func (d *dummyClient) PutSequenced(name upspin.PathName, seq int64, data []byte) (*upspin.DirEntry, error) {
	d.putData = make([]byte, len(data))
	copy(d.putData, data)
	return nil, nil
}
func (d *dummyClient) PutLink(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) PutDuplicate(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) Delete(name upspin.PathName) error {
	return nil
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
func (d *dummyClient) DirServer(name upspin.PathName) (upspin.DirServer, error) {
	return nil, nil
}
func (d *dummyClient) Rename(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, nil
}
func (d *dummyClient) SetTime(name upspin.PathName, t upspin.Time) error {
	return nil
}
