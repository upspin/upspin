// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package ose is a version of the file ops from the os package using encrypted files.
This version uses on disk files with a simple block encryption scheme to allow
random access of the file. Each 32 byte block of the file is encrypted by xoring
the contents with the AES encryption of the block number. Keys are per file and
kept in memory.

This enryption provides  secrecy for files on lost machines but no integrity since
any contents can be changed with impunity.

The arguments to exported functions are the same as the equivalent os pkg functions.
*/

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

const (
	O_RDONLY = os.O_RDONLY // open the file read-only.
	O_WRONLY = os.O_WRONLY // open the file write-only.
	O_RDWR   = os.O_RDWR   // open the file read-write.
	O_APPEND = os.O_APPEND // append data to the file when writing.
	O_CREATE = os.O_CREATE // create a new file if none exists.
	O_EXCL   = os.O_EXCL   // used with O_CREATE, file must not exist
	O_SYNC   = os.O_SYNC   // open for synchronous I/O.
	O_TRUNC  = os.O_TRUNC  // if possible, truncate file when opened.
)

var state = struct {
	sync.Mutex
	mapping map[string]*File
}{mapping: make(map[string]*File)}

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
	state.Lock()
	defer state.Unlock()
	file, ok := state.mapping[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		state.mapping[name] = file
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
	state.Lock()
	defer state.Unlock()
	file, ok := state.mapping[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		state.mapping[name] = file
	}
	file.f = f
	file.refs++
	return file, nil
}

// Rename renames file 'from' to 'to'.
func Rename(from, to string) error {
	state.Lock()
	defer state.Unlock()
	file, ok := state.mapping[from]
	if !ok {
		return fmt.Errorf("old file doesn't exist: %s", from)
	}
	_, ok = state.mapping[to]
	if ok {
		return fmt.Errorf("new file exists: %s", to)
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	delete(state.mapping, from)
	state.mapping[to] = file
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
	state.Lock()
	defer state.Unlock()
	file.refs--
	if file.refs != 0 {
		return nil
	}
	delete(state.mapping, file.name)
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
