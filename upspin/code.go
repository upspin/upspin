// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"encoding/binary"
	"errors" // Cannot use Upspin's error package because it would introduce a dependency cycle.
	"fmt"
	"time"
)

// This file contains implementations of things like marshaling of the
// basic Upspin types.

// ErrNoConfiguration is returned by the Configure method of the NoConfiguration type.
var ErrNoConfiguration = errors.New("service does not accept configuration options")

// NoConfiguration is a trivial type that implements the Configure method by returning
// ErrNoConfiguration. It can be embedded in a type for a simple way to disable
// configuration options for the service.
type NoConfiguration struct{}

// Configure implements Service.
func (NoConfiguration) Configure(options ...string) error {
	return ErrNoConfiguration
}

// Marshal packs the DirBlock into a byte slice for transport.
func (d *DirBlock) Marshal() ([]byte, error) {
	return d.MarshalAppend(nil)
}

// MarshalAppend packs the DirBlock and appends it onto the given
// byte slice for transport. It will create a new slice if b is nil
// and grow the slice if necessary. However, if b's capacity is large
// enough, MarshalAppend will do no allocation. If it does allocate,
// the returned slice will point to different storage than does the
// input argument, as with the built-in append function.
func (d *DirBlock) MarshalAppend(b []byte) ([]byte, error) {
	var tmp [16]byte // For use by PutVarint and PutUvarint.

	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count n followed by n bytes.
	b = append(b, byte(d.Location.Endpoint.Transport))
	b = appendString(b, string(d.Location.Endpoint.NetAddr))
	// Location.Key:
	//	Reference: count n followed by n bytes.
	b = appendString(b, string(d.Location.Reference))

	// Offset
	n := binary.PutVarint(tmp[:], d.Offset)
	b = append(b, tmp[:n]...)

	// Size
	n = binary.PutVarint(tmp[:], d.Size)
	b = append(b, tmp[:n]...)

	// Packdata
	b = appendBytes(b, d.Packdata)

	return b, nil
}

// Unmarshal unpacks the byte slice to recover the encoded DirBlock.
func (d *DirBlock) Unmarshal(b []byte) ([]byte, error) {
	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count n followed by n bytes.
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Location.Endpoint.Transport = Transport(b[0])
	b = b[1:]
	var bytes []byte
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Location.Endpoint.NetAddr = NetAddr(bytes)

	// d.Location.Reference
	//	Packing: 1 byte.
	//	Key: count h followed by h bytes.
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Location.Reference = Reference(bytes)

	// Offset.
	offset, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	d.Offset = offset
	b = b[n:]

	// Size.
	size, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	d.Size = size
	b = b[n:]

	// Must copy Packdata - can't return buffer's own contents.
	// (All the other slices are turned into strings, so are intrinsically copied.)
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Packdata = make([]byte, len(bytes))
	copy(d.Packdata, bytes)

	return b, nil
}

// Size returns the total length of the data underlying the DirEntry
// and validates the block offsets and sizes. If the blocks are not
// contiguous, it returns an error, but does return the sum of
// the sizes of the underlying blocks. If a block has a negative
// size, it returns zero and an error.
func (d *DirEntry) Size() (size int64, err error) {
	for i := range d.Blocks {
		if size != d.Blocks[i].Offset && err == nil {
			err = fmt.Errorf("Size: %v: inconsistent offsets", d.Name)
		}
		sz := d.Blocks[i].Size
		if sz < 0 {
			return 0, fmt.Errorf("Size: %v: negative size", d.Name)
		}
		size += sz
	}
	return size, err
}

// Marshal packs the DirEntry into a new byte slice for transport.
func (d *DirEntry) Marshal() ([]byte, error) {
	return d.MarshalAppend(nil)
}

// MarshalAppend packs the DirEntry and appends it onto the given
// byte slice for transport. It will create a new slice if b is nil
// and grow the slice if necessary. However, if b's capacity is large
// enough, MarshalAppend will do no allocation. If it does allocate,
// the returned slice will point to different storage than does the
// input argument, as with the built-in append function.
func (d *DirEntry) MarshalAppend(b []byte) ([]byte, error) {
	var tmp [16]byte // For use by PutVarint and PutUvarint.

	// Name: count n followed by n bytes.
	b = appendString(b, string(d.Name))

	// Packing: One byte.
	b = append(b, byte(d.Packing))

	// Time.
	n := binary.PutVarint(tmp[:], int64(d.Time))
	b = append(b, tmp[:n]...)

	// Blocks.
	// First a uvarint count, then the data.
	n = binary.PutUvarint(tmp[:], uint64(len(d.Blocks)))
	b = append(b, tmp[:n]...)
	for i := range d.Blocks {
		var err error
		b, err = d.Blocks[i].MarshalAppend(b)
		if err != nil {
			return nil, err
		}
	}

	// Packdata.
	b = appendBytes(b, d.Packdata)

	// Attr: One byte.
	b = append(b, byte(d.Attr))

	// Sequence.
	n = binary.PutVarint(tmp[:], d.Sequence)
	b = append(b, tmp[:n]...)

	// Writer.
	b = appendString(b, string(d.Writer))

	return b, nil
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutUvarint.
	n := binary.PutUvarint(tmp[:], uint64(len(str)))
	b = append(b, tmp[:n]...)
	b = append(b, str...)
	return b
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutUvarint.
	n := binary.PutUvarint(tmp[:], uint64(len(bytes)))
	b = append(b, tmp[:n]...)
	b = append(b, bytes...)
	return b
}

// ErrTooShort is returned by Unmarshal methods if the data is incomplete.
var ErrTooShort = errors.New("Unmarshal buffer too short")

// Unmarshal unpacks a marshaled DirEntry and stores it in the receiver.
// If successful, every field of d will be overwritten and the remaining
// data will be returned.
func (d *DirEntry) Unmarshal(b []byte) ([]byte, error) {
	// Name: count N followed by N bytes.
	bytes, b := getBytes(b)
	if len(b) < 1 { // Check for packing here too.
		return nil, ErrTooShort
	}
	d.Name = PathName(bytes)

	// Packing: One byte.
	d.Packing = Packing(b[0])
	b = b[1:]

	// Time.
	time, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	d.Time = Time(time)
	b = b[n:]

	// Blocks. First a uvarint count, then the blocks.
	nBlocks, n := binary.Uvarint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	b = b[n:]
	d.Blocks = make([]DirBlock, nBlocks)
	for i := range d.Blocks {
		var err error
		b, err = d.Blocks[i].Unmarshal(b)
		if err != nil {
			return nil, err
		}
	}

	// Packdata.
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	// Must copy the data for Packdata - can't return buffer's own contents.
	// (Most other slices are turned into strings, so are intrinsically copied.)
	d.Packdata = make([]byte, len(bytes))
	copy(d.Packdata, bytes)

	// Attr: One byte.
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Attr = FileAttributes(b[0])
	b = b[1:]

	// Sequence.
	seq, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	d.Sequence = seq
	b = b[n:]

	// Writer.
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Writer = UserName(bytes)

	return b, nil
}

// getBytes unmarshals the byte slice at b (uvarint count followed by bytes)
// and returns the slice followed by the remaining bytes.
// If there is insufficient data, both return values will be nil.
func getBytes(b []byte) (data, remaining []byte) {
	u, n := binary.Uvarint(b)
	if n == 0 || len(b) < n+int(u) {
		return nil, nil
	}
	return b[n : n+int(u)], b[n+int(u):]
}

// String returns a default string representation of the time,
// in the format similar to RFC 3339: "2006-01-02T15:04:05 UTC"
// The time zone always UTC.
func (t Time) String() string {
	return t.Go().Format("2006-01-02T15:04:05 UTC")
}

// Go returns the Go Time value representation of an Upspin time.
// The time zone always UTC.
func (t Time) Go() time.Time {
	return time.Unix(int64(t), 0).In(time.UTC)
}

// TimeFromGo returns the Upspin Time value representation of a Go time.
func TimeFromGo(t time.Time) Time {
	return Time(t.Unix())
}

// Now returns the current Upspin Time.
func Now() Time {
	return TimeFromGo(time.Now())
}

// IsDir reports whether the entry is a directory.
func (d *DirEntry) IsDir() bool {
	return d.Attr == AttrDirectory
}

// IsLink reports whether the entry is a link to
// something perhaps outside of Upspin.
func (d *DirEntry) IsLink() bool {
	return d.Attr == AttrLink
}

// ErrIncompatible is returned by SetDir and SetRedirect to indicate the
// current attribute bits are incompatible with a directory or redirect.
var ErrIncompatible = errors.New("attribute incompatible with directory entry")

// SetDir marks this entry as a directory. If any other bits are set,
// it is an error.
func (d *DirEntry) SetDir() error {
	if d.Attr|AttrDirectory != AttrDirectory {
		return ErrIncompatible
	}
	d.Attr = AttrDirectory
	return nil
}

// SetLink marks this entry as a link. If any other bits are set,
// it is an error.
func (d *DirEntry) SetLink() error {
	if d.Attr|AttrLink != AttrLink {
		return ErrIncompatible
	}
	d.Attr = AttrLink
	return nil
}

func (p Packing) String() string {
	switch p {
	case PlainPack:
		return "plain"
	case DebugPack:
		return "debug"
	case EEPack:
		return "ee"
	default:
		return fmt.Sprintf("packing(%d)", int(p))
	}
}

func (t Transport) String() string {
	switch t {
	case Unassigned:
		return "unassigned"
	case InProcess:
		return "inprocess"
	case GCP:
		return "gcp"
	case Remote:
		return "remote"
	case HTTPS:
		return "https"
	default:
		return fmt.Sprintf("transport(%d)", int(t))
	}
}
