// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

/*
Package ose is a version of the file ops from the os package using encrypted files.
This version uses on disk files with a simple block encryption scheme to allow
random access of the file. Each 32 byte block of the file is encrypted by xoring
the contents with the AES encryption of the block number. Keys are per file and
kept in memory.

This encryption provides secrecy for files on lost machines but no integrity since
any contents can be changed with impunity.

The arguments to exported functions are the same as the equivalent os pkg functions.
*/

package ose // import "upspin.io/cmd/upspinfs/internal/ose"

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"

	"upspin.io/cache"

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

	// maxUnopenedCached is the maximum number of unopened files cached.
	maxUnopenedCached = 200
)

var state = struct {
	sync.Mutex
	nameToFile map[string]*File
	toRemove   *cache.LRU
}{
	nameToFile: make(map[string]*File),
	toRemove:   cache.NewLRU(maxUnopenedCached),
}

// File represents an encrypted file.
// There are no holes, in other words the disk file is not sparse.
type File struct {
	name string
	f    *os.File
	benc cipher.Block
	refs int
	size int64
}

// OpenFile opens an encrypted file.
func OpenFile(name string, flag int, mode os.FileMode) (*File, error) {
	f, err := os.OpenFile(name, flag, mode)
	if err != nil {
		return nil, err
	}
	state.Lock()
	defer state.Unlock()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	file, ok := state.nameToFile[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		state.nameToFile[name] = file
	}
	file.f = f
	file.refs++
	file.size = st.Size()
	state.toRemove.Remove(file.name)
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
	file, ok := state.nameToFile[name]
	if ok {
		file.f.Close()
	} else {
		benc, err := newBenc()
		if err != nil {
			return nil, err
		}
		file = &File{name: name, benc: benc}
		state.nameToFile[name] = file
	}
	file.f = f
	file.refs++
	file.size = 0
	state.toRemove.Remove(file.name)
	return file, nil
}

// Rename renames file 'from' to 'to'.
func Rename(from, to string) error {
	state.Lock()
	defer state.Unlock()
	file, ok := state.nameToFile[from]
	if !ok {
		return fmt.Errorf("old file doesn't exist: %s", from)
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	delete(state.nameToFile, from)
	state.nameToFile[to] = file
	file.name = to
	state.toRemove.Remove(file.name)
	return nil
}

// Mkdir creates the named directory.
func Mkdir(name string, mode os.FileMode) error {
	return os.Mkdir(name, mode)
}

// MkdirAll creates the named directory and all path elements leading to it.
func MkdirAll(name string, mode os.FileMode) error {
	return os.MkdirAll(name, mode)
}

// Remove removes the named file.
func Remove(name string) error {
	state.Lock()
	defer state.Unlock()
	delete(state.nameToFile, name)
	return os.Remove(name)
}

// RemoveAll is a recursive remove.
func RemoveAll(subtree string) error {
	return os.RemoveAll(subtree)
}

// Truncate shortens a file.
func Truncate(name string, size int64) error {
	state.Lock()
	defer state.Unlock()
	file, ok := state.nameToFile[name]
	if !ok {
		return fmt.Errorf("file to truncate doesn't exist: %s", name)
	}
	err := os.Truncate(name, size)
	if err == nil {
		file.size = size
	}
	return err
}

// Close closes a file. If the ref count goes to zero, the file is removed.
func (file *File) Close() error {
	state.Lock()
	defer state.Unlock()
	file.refs--
	if file.refs != 0 {
		return nil
	}
	state.toRemove.Add(file.name, evictable(true))
	return file.f.Close()
}

// Pin increases the reference count so the file will not be removed.
func (file *File) Pin() {
	state.Lock()
	file.refs++
	state.Unlock()
}

// Stat returns the status of a file.
func (file *File) Stat() (os.FileInfo, error) {
	return file.f.Stat()
}

// ReadAt performs a random access read of an encrypted file and returns the
// decrypted content.
func (file *File) ReadAt(b []byte, off int64) (int, error) {
	n, err := file.f.ReadAt(b, off)
	if n > 0 {
		file.xor(b[:n], off)
	}
	return n, err
}

// WriteAt encrypts the content and writes it to the file.
// Unlike os.WriteAt, this changes the contents of b.
func (file *File) WriteAt(b []byte, off int64) (int, error) {
	if off > file.size {
		// This WriteAt implicitly extends the file. Fill the hole.
		state.Lock()
		for off > file.size {
			m := off - file.size
			if m > 1024*1024 {
				m = 1024 * 1024
			}
			hole := make([]byte, m)
			file.xor(hole, file.size)
			n, err := file.f.WriteAt(hole, file.size)
			if err != nil {
				state.Unlock()
				return 0, err
			}
			if n == 0 {
				state.Unlock()
				return 0, fmt.Errorf("zero write filling hole, expected %d", len(hole))
			}
			file.size += int64(n)
		}
		state.Unlock()
	}
	file.xor(b, off)
	n, err := file.f.WriteAt(b, off)
	state.Lock()
	if n > 0 && file.size < off+int64(n) {
		file.size = off + int64(n)
	}
	state.Unlock()
	return n, err
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
	if off%bsize != 0 {
		binary.PutVarint(maskInput, off/bsize)
		file.benc.Encrypt(mask, maskInput)
	}
	for i := range b {
		x := (off + int64(i)) % bsize
		if x == 0 {
			binary.PutVarint(maskInput, (off+int64(i))/bsize)
			file.benc.Encrypt(mask, maskInput)
		}
		b[i] ^= mask[x]
	}
}

// A type for LRU to call when evicting an entry.
type evictable bool

func (e evictable) OnEviction(key interface{}) {
	// TODO(p): figure out where we might log errors.
	os.Remove(key.(string))
}
