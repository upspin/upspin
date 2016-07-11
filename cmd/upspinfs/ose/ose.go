// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ose

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"

	"fmt"
	"os"
	"sync"
)

// This is an emulation of some file ops in the os package. This version uses on
// disk files with simple encryption. Each file is block encrypted by xoring the contents
// with the AES encryption of the block number. Keys are per file. This allows
// random access and a modicum of privacy.
//
// The arguments to exported functions are the same as the equivalent os pkg functions.

const (
	O_RDONLY int = os.O_RDONLY // open the file read-only.
	O_WRONLY int = os.O_WRONLY // open the file write-only.
	O_RDWR   int = os.O_RDWR   // open the file read-write.
	O_APPEND int = os.O_APPEND // append data to the file when writing.
	O_CREATE int = os.O_CREATE // create a new file if none exists.
	O_EXCL   int = os.O_EXCL   // used with O_CREATE, file must not exist
	O_SYNC   int = os.O_SYNC   // open for synchronous I/O.
	O_TRUNC  int = os.O_TRUNC  // if possible, truncate file when opened.
)

var ose struct {
	sync.Mutex
	mapping map[string]*File
}

// File represents an encrypted file.
type File struct {
	name string
	f    *os.File
	benc cipher.Block
	refs int
}

// OpenFile opens an encrypted file.
func OpenFile(name string, flag int, mode os.FileMode) (*File, error) {
	f, err := os.OpenFile(name, flag, mode)
	if err != nil {
		return nil, err
	}
	ose.Lock()
	defer ose.Unlock()
	file, ok := ose.mapping[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		ose.mapping[name] = file
	}
	file.f = f
	file.refs++
	return file, nil
}

// Create creates an encrypted file.
func Create(name string) (*File, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	ose.Lock()
	defer ose.Unlock()
	file, ok := ose.mapping[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		ose.mapping[name] = file
	}
	file.f = f
	file.refs++
	return file, nil
}

// Rename renames file 'from' to 'to'.
func Rename(from, to string) error {
	ose.Lock()
	defer ose.Unlock()
	file, ok := ose.mapping[from]
	if !ok {
		return fmt.Errorf("old file doesn't exist: %s", from)
	}
	_, ok = ose.mapping[to]
	if ok {
		return fmt.Errorf("new file exists: %s", to)
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	delete(ose.mapping, from)
	ose.mapping[to] = file
	return nil
}

// Mkdir creates the named directory.
func Mkdir(name string, mode os.FileMode) error {
	return os.Mkdir(name, mode)
}

// Remove removes the named file.
func Remove(name string) error {
	return os.Remove(name)
}

// RemoveAll is a recursive remove.
func RemoveAll(subtree string) error {
	return os.RemoveAll(subtree)
}

// Close closes a file. If the ref count goes to zero, the file is removed.
func (file *File) Close() error {
	ose.Lock()
	defer ose.Unlock()
	file.refs--
	if file.refs != 0 {
		return nil
	}
	delete(ose.mapping, file.name)
	os.Remove(file.name)
	return file.f.Close()
}

// Stat returns the status of a file.
func (file *File) Stat() (os.FileInfo, error) {
	return file.f.Stat()
}

// ReadAt performs a random access read of an encrypted file and returns the
// decrypted content.
func (file *File) ReadAt(b []byte, off int64) (int, error) {
	n, err := file.f.ReadAt(b, off)
	if err != nil {
		return n, err
	}
	file.xor(b, off)
	return n, nil
}

// WriteAt encrypts the content and writes it to the file.
func (file *File) WriteAt(b []byte, off int64) (int, error) {
	file.xor(b, off)
	return file.f.WriteAt(b, off)
}

const aesKeyLen = 32

func newBenc() (cipher.Block, error) {
	k := make([]byte, aesKeyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}

	// Create an Xcrypter.
	benc, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return benc, nil
}

func (file *File) xor(b []byte, off int64) {
	bsize := int64(file.benc.BlockSize())
	mask := make([]byte, bsize)
	maskInput := make([]byte, bsize)
	end := off + int64(len(b))
	sofar := int64(0)
	if off%bsize != 0 {
		binary.PutVarint(maskInput, off/bsize)
		file.benc.Encrypt(mask, maskInput)
	}
	for off+sofar < end {
		x := (off + sofar) % bsize
		if x == 0 {
			binary.PutVarint(maskInput, (off+sofar)/bsize)
			file.benc.Encrypt(mask, maskInput)
		}
		b[sofar] ^= mask[x]
		sofar++
	}
}

func init() {
	ose.mapping = make(map[string]*File)
}
