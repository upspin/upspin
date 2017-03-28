// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin // import "upspin.io/upspin"

import (
	"crypto/rand"
	"encoding/binary"
	"errors" // Cannot use Upspin's error package because it would introduce a dependency cycle.
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file contains implementations of things like marshaling of the
// basic Upspin types.

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
	var tmp [16]byte // For use by PutVarint.

	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count n followed by n bytes.
	b = append(b, byte(d.Location.Endpoint.Transport))
	b = appendString(b, string(d.Location.Endpoint.NetAddr))
	// Location.Reference: count n followed by n bytes.
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

	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	if len(bytes) > 0 {
		// Must copy Packdata - can't return buffer's own contents.
		// (All the other slices are turned into strings, so are intrinsically copied.)
		d.Packdata = append([]byte(nil), bytes...)
	} else {
		d.Packdata = nil
	}

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
	var tmp [16]byte // For use by PutVarint.

	// SignedName: count n followed by n bytes.
	b = appendString(b, string(d.SignedName))

	// Packing: One byte.
	b = append(b, byte(d.Packing))

	// Time.
	n := binary.PutVarint(tmp[:], int64(d.Time))
	b = append(b, tmp[:n]...)

	// Blocks.
	// First a varint count, then the data.
	n = binary.PutVarint(tmp[:], int64(len(d.Blocks)))
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

	// Link.
	b = appendString(b, string(d.Link))

	// Writer.
	b = appendString(b, string(d.Writer))

	// Name: if different from SignedName, count n followed by n bytes.
	// Otherwise, count zero with no bytes following.
	if d.Name != d.SignedName {
		b = appendString(b, string(d.Name))
	} else {
		// Encode a special -1 that denotes Name == SignedName.
		n = binary.PutVarint(tmp[:], int64(-1))
		b = append(b, tmp[:n]...)
	}

	// Attr: One byte.
	b = append(b, byte(d.Attr))

	// Sequence.
	n = binary.PutVarint(tmp[:], d.Sequence)
	b = append(b, tmp[:n]...)

	return b, nil
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(str)))
	b = append(b, tmp[:n]...)
	b = append(b, str...)
	return b
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(bytes)))
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
	// SignedName: count N followed by N bytes.
	bytes, b := getBytes(b)
	if len(b) < 1 { // Check for packing here too.
		return nil, ErrTooShort
	}
	d.SignedName = PathName(bytes)

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

	// Blocks. First a varint count, then the blocks.
	nBlocks, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	b = b[n:]
	d.Blocks = nil
	if nBlocks > 0 {
		d.Blocks = make([]DirBlock, nBlocks)
		for i := range d.Blocks {
			var err error
			b, err = d.Blocks[i].Unmarshal(b)
			if err != nil {
				return nil, err
			}
		}
	}

	// Packdata.
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	if len(bytes) > 0 {
		// Must copy the data for Packdata - can't return buffer's own contents.
		// (Most other slices are turned into strings, so are intrinsically copied.)
		d.Packdata = append([]byte(nil), bytes...)
	} else {
		d.Packdata = nil
	}

	// Link: count N followed by N bytes.
	bytes, b = getBytes(b)
	d.Link = PathName(bytes) // Zero-length is OK here.

	// Writer.
	bytes, b = getBytes(b)
	if len(b) < 1 { // At least one byte for Name.
		return nil, ErrTooShort
	}
	d.Writer = UserName(bytes)

	// Name: count N followed by N bytes.
	// If N is -1 Name equals SignedName.
	length, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	b = b[n:]
	if length == -1 {
		// -1 is a special code that indicates Name == SignedName
		d.Name = d.SignedName
	} else {
		bytes, b = getNBytes(b, int(length))
		if bytes == nil {
			return nil, ErrTooShort
		}
		d.Name = PathName(bytes)
	}

	// Attr: One byte.
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Attr = Attribute(b[0])
	b = b[1:]

	// Sequence.
	seq, n := binary.Varint(b)
	if n == 0 {
		return nil, ErrTooShort
	}
	d.Sequence = seq
	b = b[n:]

	return b, nil
}

// getBytes unmarshals the byte slice at b (varint count followed by bytes)
// and returns the slice followed by the remaining bytes.
// If there is insufficient data, both return values will be nil.
func getBytes(b []byte) (data, remaining []byte) {
	u, n := binary.Varint(b)
	if u < 0 {
		return nil, nil
	}
	if n == 0 || len(b) < n+int(u) {
		return nil, nil
	}
	return getNBytes(b[n:], int(u))
}

// getNBytes unmarshals n bytes from b and returns the slice followed by the
// remaining bytes. If there is insufficient data, both return values will be
// nil.
func getNBytes(b []byte, n int) (data, remaining []byte) {
	if len(b) < n {
		return nil, nil
	}
	return b[:n], b[n:]
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

// IsRegular reports whether the entry is neither a directory nor link.
func (d *DirEntry) IsRegular() bool {
	return d.Attr&AttrDirectory == 0 &&
		d.Attr&AttrLink == 0
}

// IsDir reports whether the entry is a directory.
func (d *DirEntry) IsDir() bool {
	return d.Attr&AttrDirectory != 0
}

// IsLink reports whether the entry is a link.
func (d *DirEntry) IsLink() bool {
	return d.Attr&AttrLink != 0
}

// IsIncomplete reports whether the entry is incomplete,
// and therefore does not have valid Blocks or Packdata.
func (d *DirEntry) IsIncomplete() bool {
	return d.Attr&AttrIncomplete != 0
}

// MarkIncomplete marks this entry as incomplete
// and zeroes the Blocks and Packdata fields.
func (d *DirEntry) MarkIncomplete() {
	d.Attr |= AttrIncomplete
	d.Blocks = nil
	d.Packdata = nil
}

// Copy makes a deep copy of the entry and returns a pointer to the copy.
func (d *DirEntry) Copy() *DirEntry {
	cp := *d
	cp.Packdata = append([]byte{}, d.Packdata...)
	cp.Blocks = nil
	if len(d.Blocks) > 0 {
		cp.Blocks = make([]DirBlock, 0, len(d.Blocks))
	}
	for _, b := range d.Blocks {
		cp.Blocks = append(cp.Blocks, *b.Copy())
	}
	return &cp
}

// Copy makes a deep copy of the block and returns a pointer to the copy.
func (d *DirBlock) Copy() *DirBlock {
	cp := *d
	cp.Packdata = append([]byte{}, d.Packdata...)
	return &cp
}

func (p Packing) String() string {
	switch p {
	case PlainPack:
		return "plain"
	case EEPack:
		return "ee"
	case EEIntegrityPack:
		return "eeintegrity"
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
	case Remote:
		return "remote"
	default:
		return fmt.Sprintf("transport(%d)", int(t))
	}
}

// Sorting []*DirEntry by name.

type dirEntrySlice []*DirEntry

func (d dirEntrySlice) Len() int           { return len(d) }
func (d dirEntrySlice) Less(i, j int) bool { return d[i].Name < d[j].Name }
func (d dirEntrySlice) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }

// SortDirEntries does an in-place sort of the slice of DirEntries, sorting them in
// increasing lexical order by Name. The boolean flag specifies whether to elide
// identical entries, that is, whether the result should contain entries with
// unique names only. (Other fields of the DirEntries are ignored.) The return
// value is the resulting slice, which shares storage with the original but may be
// shorter if unique is true.
func SortDirEntries(slice []*DirEntry, unique bool) []*DirEntry {
	sort.Sort(dirEntrySlice(slice))
	if !unique {
		return slice
	}
	result := make([]*DirEntry, 0, len(slice))
	for i, entry := range slice {
		if i == 0 || entry.Name != slice[i-1].Name {
			result = append(result, entry)
		}
	}
	return result
}

// NewSequence creates a sequence for a new directory entry.
//
// The sequence is intended to be different for each Put of a
// Pathname. We achieve this by
// - incrementing the sequence on every Put of an existing DirEntry.
// - choosing a unique value for the top 41 bits of the sequence on a
//   Put of a new DirEntry.
//
// The unique value is provided by a cryptographic random number generator if one is
// available. Failing that, we use nanosecond time.
func NewSequence() int64 {
	x := make([]byte, 5)
	_, err := rand.Read(x)
	var s int64
	if err == nil {
		s = int64(x[0])<<32 | int64(x[1])<<24 | int64(x[2])<<16 | int64(x[3])<<8 | int64(x[4])
	} else {
		s = (time.Now().UnixNano() & 0xffffffffff)
	}
	return (s << 23) | SeqBase
}

// SeqVersion returns the version part of a sequence number. It is incremented
// on every Put of an existing DirEntry and wraps at 2^23.
func SeqVersion(s int64) int64 {
	return s & 0x7fffff
}

// SeqNext returns the next sequence number. It wraps to avoid values less than SeqBase.
func SeqNext(s int64) int64 {
	s++
	if s < SeqBase {
		return SeqBase
	}
	return s
}

func isMeta(b byte) bool {
	switch b {
	case '\\', '*', '?', '[':
		return true
	default:
		return false
	}
}

// QuoteGlob returns a string that quotes all Glob metacharacters
// inside the argument path name; the returned string is a regular expression matching
// the literal text. For example, QuoteGlob`[foo]`) returns `\[foo\]`.
// In effect, the resulting string matches the literal text of the argument
// with no metacharacter processing.
func QuoteGlob(p PathName) PathName {
	s := string(p)
	// A byte loop is correct because all metacharacters are ASCII.
	found := false
	for i := 0; i < len(s); i++ {
		if isMeta(s[i]) {
			found = true
			break
		}
	}
	if !found {
		return p
	}

	b := make([]byte, 0, 2*len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isMeta(c) {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return PathName(b)
}

// AllFilesGlob returns the Glob pattern that will match all the files in the
// argument directory, which is treated as a literal string (that is, it is passed
// through QuoteGlob). For example, given ann@machine.com/foo, it will
// return ann@machine.com/foo/*.
func AllFilesGlob(dir PathName) string {
	str := string(QuoteGlob(dir))
	// Avoid doubling a final slash.
	if strings.HasSuffix(str, "/") {
		return str + "*"
	}
	return str + "/*"
}
