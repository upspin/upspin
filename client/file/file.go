// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package file implements the File interface used in client.Open and client.Create.
package file // import "upspin.io/client/file"

import (
	"io"

	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/upspin"
)

// maxInt is the int64 representation of the maximum value of an int.
// It allows us to verify that an int64 value never exceeds the length of a slice.
// In the tests, we cut it down to manageable size for overflow checking.
var maxInt = int64(^uint(0) >> 1)

// File is a simple implementation of upspin.File.
// It always keeps the whole file in memory under the assumption
// that it is encrypted and must be read and written atomically.
type File struct {
	name     upspin.PathName // Full path name.
	offset   int64           // File location for next read or write operation. Constrained to <= maxInt.
	writable bool            // File is writable (made with Create, not Open).
	closed   bool            // Whether the file has been closed, preventing further operations.

	// Used only by readers.
	config upspin.Config
	entry  *upspin.DirEntry
	size   int64
	bu     upspin.BlockUnpacker
	// Keep the most recently unpacked block around
	// in case a subsequent readAt starts at the same place.
	lastBlockIndex int
	lastBlockBytes []byte

	// Used only by writers.
	client upspin.Client // Client the File belongs to.
	data   []byte        // Contents of file.
}

var _ upspin.File = (*File)(nil)

// Readable creates a new File for the given DirEntry that must be readable
// using the given Config.
func Readable(cfg upspin.Config, entry *upspin.DirEntry) (*File, error) {
	// TODO(adg): check if this is a dir or link?
	const op errors.Op = "client/file.Readable"

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.E(op, entry.Name, errors.Invalid, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(cfg, entry)
	if err != nil {
		return nil, errors.E(op, entry.Name, err)
	}
	size, err := entry.Size()
	if err != nil {
		return nil, errors.E(op, entry.Name, err)
	}

	return &File{
		config:         cfg,
		name:           entry.Name,
		writable:       false,
		entry:          entry,
		size:           size,
		bu:             bu,
		lastBlockIndex: -1,
	}, nil
}

// Writable creates a new file with a given name, belonging to a given
// client for write. Once closed, the file will overwrite any existing
// file with the same name.
func Writable(client upspin.Client, name upspin.PathName) *File {
	return &File{
		client:   client,
		name:     name,
		writable: true,
	}
}

// Name implements upspin.File.
func (f *File) Name() upspin.PathName {
	return f.name
}

// Read implements upspin.File.
func (f *File) Read(b []byte) (n int, err error) {
	const op errors.Op = "file.Read"
	n, err = f.readAt(op, b, f.offset)
	if err == nil {
		f.offset += int64(n)
	}
	return n, err
}

// ReadAt implements upspin.File.
func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
	const op errors.Op = "file.ReadAt"
	return f.readAt(op, b, off)
}

func (f *File) readAt(op errors.Op, dst []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, f.errClosed(op)
	}
	if f.writable {
		return 0, errors.E(op, errors.Invalid, f.name, "not open for read")
	}
	if off < 0 {
		return 0, errors.E(op, errors.Invalid, f.name, "negative offset")
	}
	if off > f.size {
		return 0, errors.E(op, errors.Invalid, f.name, errors.Errorf("offset (%d) beyond end of file (%d)", off, f.size))
	}
	if off == f.size {
		return 0, io.EOF
	}

	// Iterate over blocks that contain the data we're interested in,
	// and unpack and copy the data to dst.
	for i := range f.entry.Blocks {
		b := &f.entry.Blocks[i]

		if b.Offset+b.Size < off {
			// This block is before our interest.
			continue
		}
		if b.Offset >= off+int64(len(dst)) {
			// This block is beyond our interest.
			break
		}

		if _, ok := f.bu.SeekBlock(i); !ok {
			return 0, errors.E(op, errors.IO, f.name, errors.Errorf("could not seek to block %d", i))
		}

		var clear []byte
		if i == f.lastBlockIndex {
			// If this is the block we last read (will often happen
			// with sequential reads) then use that content,
			// to avoid reading and unpacking again.
			// TODO(adg): write a test to ensure this is happening.
			clear = f.lastBlockBytes
		} else {
			// Otherwise, we need to read the block and unpack.
			cipher, err := clientutil.ReadLocation(f.config, b.Location)
			if err != nil {
				return 0, errors.E(op, errors.IO, f.name, err)
			}
			clear, err = f.bu.Unpack(cipher)
			if err != nil {
				return 0, errors.E(op, errors.IO, f.name, err)
			}
			f.lastBlockIndex = i
			f.lastBlockBytes = clear
		}

		clearIdx := 0
		if off > b.Offset {
			clearIdx = int(off - b.Offset)
		}
		n += copy(dst[n:], clear[clearIdx:])
	}

	return n, nil
}

// Seek implements upspin.File.
func (f *File) Seek(offset int64, whence int) (ret int64, err error) {
	const op errors.Op = "file.Seek"
	if f.closed {
		return 0, f.errClosed(op)
	}
	switch whence {
	case 0:
		ret = offset
	case 1:
		ret = f.offset + offset
	case 2:
		if f.writable {
			ret = int64(len(f.data)) + offset
		} else {
			ret = f.size + offset
		}
	default:
		return 0, errors.E(op, errors.Invalid, f.name, "bad whence")
	}
	if ret < 0 || offset > maxInt || !f.writable && ret > f.size {
		return 0, errors.E(op, errors.Invalid, f.name, "bad offset")
	}
	f.offset = ret
	return ret, nil
}

// Write implements upspin.File.
func (f *File) Write(b []byte) (n int, err error) {
	const op errors.Op = "file.Write"
	n, err = f.writeAt(op, b, f.offset)
	if err == nil {
		f.offset += int64(n)
	}
	return n, err
}

// WriteAt implements upspin.File.
func (f *File) WriteAt(b []byte, off int64) (n int, err error) {
	const op errors.Op = "file.WriteAt"
	return f.writeAt(op, b, off)
}

func (f *File) writeAt(op errors.Op, b []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, f.errClosed(op)
	}
	if !f.writable {
		return 0, errors.E(op, errors.Invalid, f.name, "not open for write")
	}
	if off < 0 {
		return 0, errors.E(op, errors.Invalid, f.name, "negative offset")
	}
	end := off + int64(len(b))
	if end > maxInt {
		return 0, errors.E(op, errors.Invalid, f.name, "file too long")
	}
	if end > int64(cap(f.data)) {
		// Grow the capacity of f.data but keep length the same.
		// Be careful not to ask for more than an int's worth of length.
		nLen := end * 3 / 2
		if nLen > maxInt {
			nLen = maxInt
		}
		ndata := make([]byte, len(f.data), nLen)
		copy(ndata, f.data)
		f.data = ndata
	}
	// Capacity is OK now. Fix the length if necessary.
	if end > int64(len(f.data)) {
		f.data = f.data[:end]
	}
	copy(f.data[off:], b)
	return len(b), nil
}

// Close implements upspin.File.
func (f *File) Close() error {
	const op errors.Op = "file.Close"
	if f.closed {
		return f.errClosed(op)
	}
	f.closed = true
	if !f.writable {
		f.lastBlockIndex = -1
		f.lastBlockBytes = nil
		if err := f.bu.Close(); err != nil {
			return errors.E(op, err)
		}
		return nil
	}
	_, err := f.client.Put(f.name, f.data)
	f.data = nil // Might as well release it early.
	return err
}

func (f *File) errClosed(op errors.Op) error {
	return errors.E(op, errors.Invalid, f.name, "is closed")
}
