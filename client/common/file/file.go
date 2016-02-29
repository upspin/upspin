// Package file implements the File interface used in client.Open and client.Create.
package file

import (
	"errors"
	"fmt"
	"io"

	"upspin.googlesource.com/upspin.git/upspin"
)

// maxInt is the int64 representation of the maximum value of an int.
// It allows us to verify that an int64 value never exceeds the length of a slice.
// In the tests, we cut it down to manageable size for overflow checking.
var maxInt = int64(^uint(0) >> 1)

// File is a simple implementation of upspin.File.
// It always keeps the whole file in memory under the assumption
// that it is encrypted and must be read and written atomically.
type File struct {
	client   upspin.Client   // Client the File belongs to.
	closed   bool            // Whether the file has been closed, preventing further operations.
	name     upspin.PathName // Full path name.
	writable bool            // File is writable (made with Create, not Open).
	offset   int64           // File location for next read or write operation. Constrained to <= maxInt.
	data     []byte          // Contents of file.
}

var _ upspin.File = (*File)(nil)

// New creates a new file with a given name and data contents,
// belonging to a given client for read only. The data belongs to File
// and must not be modified after this call.
func Readable(client upspin.Client, name upspin.PathName, data []byte) *File {
	return &File{
		client:   client,
		name:     name,
		writable: false,
		data:     data,
	}
}

// New creates a new file with a given name, belonging to a given
// client for write.
func Writable(client upspin.Client, name upspin.PathName) *File {
	return &File{
		client:   client,
		name:     name,
		writable: true,
	}
}

func (f *File) Name() upspin.PathName {
	return f.name
}

func (f *File) Read(b []byte) (n int, err error) {
	n, err = f.readAt("Read", b, f.offset)
	if err == nil {
		f.offset += int64(n)
	}
	return n, err
}

func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
	return f.readAt("ReadAt", b, off)
}

func (f *File) readAt(op string, b []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, f.errClosed(op)
	}
	if f.writable {
		return 0, fmt.Errorf("%s: %q is not open for read", op, f.name)
	}
	if off < 0 {
		return 0, fmt.Errorf("%s: %q: negative offset", op, f.name)
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n = copy(b, f.data[off:])
	return n, nil
}

func (f *File) Seek(offset int64, whence int) (ret int64, err error) {
	if f.closed {
		return 0, f.errClosed("Seek")
	}
	switch whence {
	case 0:
		ret = offset
	case 1:
		ret = f.offset + offset
	case 2:
		ret = int64(len(f.data)) + offset
	default:
		return 0, errors.New("bad seek whence")
	}
	if ret < 0 || offset > maxInt {
		return 0, errors.New("bad seek offset")
	}
	f.offset = ret
	return ret, nil
}

func (f *File) Write(b []byte) (n int, err error) {
	n, err = f.writeAt("Write", b, f.offset)
	if err == nil {
		f.offset += int64(n)
	}
	return n, err
}

func (f *File) WriteAt(b []byte, off int64) (n int, err error) {
	return f.writeAt("WriteAt", b, off)
}

func (f *File) writeAt(op string, b []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, f.errClosed(op)
	}
	if !f.writable {
		return 0, fmt.Errorf("%s: %q is not open for write", op, f.name)
	}
	if off < 0 {
		return 0, fmt.Errorf("%s: %q: negative offset", op, f.name)
	}
	end := off + int64(len(b))
	if end > maxInt {
		return 0, fmt.Errorf("%s: %q: file too long", op, f.name)
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

func (f *File) Close() error {
	if f.closed {
		return f.errClosed("Close")
	}
	f.closed = true
	if !f.writable {
		f.data = nil // Might as well release it early.
		return nil
	}
	_, err := f.client.Put(f.name, f.data)
	f.data = nil // Might as well release it early.
	return err
}

func (f *File) errClosed(op string) error {
	return fmt.Errorf("%s: %q is closed", op, f.name)
}
