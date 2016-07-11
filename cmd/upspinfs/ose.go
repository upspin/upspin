// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"

	"fmt"
	"os"
	"sync"
)

// This is an emulation of the some file ops in the os package. This version uses on
// disk files with simple encryption. Each file is block encryoted by xoring the contents
// with the AES encryption of the block number. Keys are per file. This allows
// random access and a modicum of privacy.
//
// If we decide that everything can fit in memory, this can be replaced by an in memory
// store.

var ose struct {
	sync.Mutex
	mapping map[string]*File
}

type File struct {
	name string
	f    *os.File
	benc cipher.Block
	refs int
}

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
		file = &File{name: name, benc: newBenc()}
		ose.mapping[name] = file
	}
	file.f = f
	file.refs++
	return file, nil
}

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

func (file *File) Stat() (os.FileInfo, error) {
	return file.f.Stat()
}

func (file *File) ReadAt(b []byte, off int64) (int, error) {
	n, err := file.f.ReadAt(b, off)
	if err != nil {
		return n, err
	}
	file.xor(b, off)
	return n, nil
}

func (file *File) WriteAt(b []byte, off int64) (int, error) {
	file.xor(b, off)
	return file.f.WriteAt(b, off)
}

const aesKeyLen = 32

func newBenc() cipher.Block {
	k := make([]byte, aesKeyLen)
	if _, err := rand.Read(k); err != nil {
		fmt.Printf("oops 1")
	}

	// Create an Xcrypter.
	benc, err := aes.NewCipher(k)
	if err != nil {
		fmt.Printf("oops 2")
	}
	return benc
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
