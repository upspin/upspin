package testclient

import (
	"errors"
	"fmt"
	"io"

	"upspin.googlesource.com/upspin.git/upspin"
)

func (c *Client) Create(name upspin.PathName) (upspin.File, error) {
	// TODO: Make sure directory exists?
	f := &File{
		client:   c,
		writable: true,
		name:     name,
	}
	return f, nil
}

func (c *Client) Open(name upspin.PathName) (upspin.File, error) {
	f := &File{
		client:   c,
		writable: false,
		name:     name,
	}
	data, err := f.client.Get(f.name)
	if err != nil {
		return nil, err
	}
	f.data = data
	return f, nil
}

// FIle is a test implementation of upspin.File.
// It always keeps the whole file in memory under the assumption
// that it is encrypted and must be read and written atomically.
type File struct {
	client   *Client         // Client the File belongs to.
	closed   bool            // Whether the file has been closed, preventing further operations.
	name     upspin.PathName // Full path name.
	writable bool            // File is writable (made with Create, not Open).
	offset   int             // File location for next read or write operation.
	data     []byte          // Contents of file.
}

var _ upspin.File = (*File)(nil)

func (f *File) Name() upspin.PathName {
	return f.name
}

func (f *File) Read(b []byte) (n int, err error) {
	n, err = f.readAt("Read", b, int64(f.offset))
	if err == nil {
		// TODO: overflow
		f.offset += n
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
		ret = int64(f.offset) + offset
	case 2:
		ret = int64(len(f.data)) + offset
	default:
		return 0, errors.New("bad seek whence")
	}
	// TODO: Do we zero-fill?
	if ret < 0 || ret > int64(len(f.data)) {
		return 0, errors.New("bad seek offset")
	}
	// TODO: overflow
	f.offset = int(ret)
	return ret, nil
}

func (f *File) Write(b []byte) (n int, err error) {
	n, err = f.writeAt("Write", b, int64(f.offset))
	if err == nil {
		// TODO: overflow
		f.offset += n
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
	// TODO: overflow
	end := int(off) + len(b)
	if end > cap(f.data) {
		// Grow the capacity of f.data but keep length the same.
		ndata := make([]byte, len(f.data), end*3/2)
		copy(ndata, f.data)
		f.data = ndata
	}
	// Capacity is OK now. Fix the length if necessary.
	if end > len(f.data) {
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
	_, err := f.client.Put(f.name, f.data, nil)
	f.data = nil // Might as well release it early.
	return err
}

func (f *File) errClosed(op string) error {
	return fmt.Errorf("%s: %q is closed", op, f.name)
}
