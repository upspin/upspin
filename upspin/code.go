// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains implementations of things like marshaling of the
// basic Upspin types.

package upspin // import "upspin.io/upspin"

import (
	"encoding/binary"
	"errors" // Cannot use Upspin's errors package because it would introduce a dependency cycle.
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// maxDirBlocks specifies a theoretical upper limit for the number of
	// DirBlocks in a DirEntry. It should be high enough that the limit
	// will never be reached in practice. 1 million seems okay.
	maxDirBlocks = 1000000
)

// This code is very careful not to grow a buffer of length more than a 32-bit
// int when marshaling data for transmission, possibly to a machine where ints
// only go to 2**31-1.
var maxInt32 uint64 = 1<<31 - 1 // Should be const, but edited by tests.

// accumulator is a helper type for marshaling data.
// It manages the buffering and keeps track of the length
// of the marshaled data for error checking.
type accumulator struct {
	buf      []byte   // The marshaled data to be returned.
	tmp      [16]byte // For use by PutVarint.
	count    uint64   // The number of bytes written.
	overflow bool     // Whether the length has overflowed an int32.
}

// byte appends a single byte.
func (acc *accumulator) byte(b byte) {
	if acc.overflow {
		return
	}
	acc.count++
	if acc.count > maxInt32 {
		acc.overflow = true
		return
	}
	acc.buf = append(acc.buf, b)
}

// string appends a string as a varint length followed by the data.
func (acc *accumulator) string(str string) {
	if acc.overflow {
		return
	}
	n := binary.PutVarint(acc.tmp[:], int64(len(str)))
	acc.count += uint64(n + len(str))
	if acc.count > maxInt32 {
		acc.overflow = true
		return
	}
	acc.buf = append(acc.buf, acc.tmp[:n]...)
	acc.buf = append(acc.buf, str...)
}

// bytes appends a byte slice as a varint length followed by the data.
func (acc *accumulator) bytes(bytes []byte) {
	if acc.overflow {
		return
	}
	n := binary.PutVarint(acc.tmp[:], int64(len(bytes)))
	acc.count += uint64(n + len(bytes))
	if acc.count > maxInt32 {
		acc.overflow = true
		return
	}
	acc.buf = append(acc.buf, acc.tmp[:n]...)
	acc.buf = append(acc.buf, bytes...)
}

// int64 appends an int64.
func (acc *accumulator) int64(i int64) {
	if acc.overflow {
		return
	}
	n := binary.PutVarint(acc.tmp[:], i)
	acc.count += uint64(n)
	if acc.count > maxInt32 {
		acc.overflow = true
		return
	}
	acc.buf = append(acc.buf, acc.tmp[:n]...)
}

// result returns the resulting slice. If it is too long, it returns ErrTooLarge.
func (acc *accumulator) result() ([]byte, error) {
	if acc.overflow {
		return nil, ErrTooLarge
	}
	return acc.buf, nil
}

// consumer is a helper type for unmarshaling data.
// It tracks the buffer and simplifies error handling.
type consumer struct {
	buf []byte // The marshaled data to be unpacked.
	err error  // First error that occured.
}

// byte unpacks a single byte.
func (c *consumer) byte() byte {
	if c.err != nil {
		return 0
	}
	if len(c.buf) == 0 {
		c.err = ErrTooShort
		return 0
	}
	b := c.buf[0]
	c.buf = c.buf[1:]
	return b
}

// bytes unpacks a byte slice.
func (c *consumer) bytes() []byte {
	if c.err != nil {
		return c.buf[:0] // Not nil, so we can convert to string etc.
	}
	u, n := binary.Varint(c.buf)
	// If n <= 0, Varint returned an error. Otherwise we know n <= len(b).
	// We also test that u is good and u bytes remain in the buffer after the count.
	if n <= 0 || u < 0 || len(c.buf[n:]) < int(u) {
		c.err = ErrTooShort
		return c.buf[:0]
	}
	if uint64(u) > maxInt32 {
		c.err = ErrTooLarge
		return c.buf[:0]
	}
	c.buf = c.buf[n:]
	data := c.buf[:u]
	c.buf = c.buf[u:]
	return data
}

// bytes unpacks a bit slice of length n, already recovered.
func (c *consumer) nBytes(n int) []byte {
	if c.err != nil {
		return c.buf[:0] // Not nil, so we can convert to string etc.
	}
	if n < 0 {
		c.err = ErrTooShort
		return c.buf[:0]
	}
	if len(c.buf) < n || maxInt32 < uint64(n) {
		c.err = ErrTooLarge
		return c.buf[:0]
	}
	data := c.buf[:n]
	c.buf = c.buf[n:]
	return data
}

// int64 unpacks a varint-encoded int64.
func (c *consumer) int64() int64 {
	if c.err != nil {
		return 0
	}
	i, n := binary.Varint(c.buf)
	switch {
	case n == 0:
		c.err = ErrTooShort
		return 0
	case n < 0:
		c.err = errors.New("integer overflow")
		return 0
	}
	c.buf = c.buf[n:]
	return i
}

// remainder returns the remaining data and the first error encountered.
func (c *consumer) remainder() ([]byte, error) {
	return c.buf, c.err
}

// Marshal packs the DirBlock into a byte slice for transport.
func (d *DirBlock) Marshal() ([]byte, error) {
	return d.MarshalAppend(nil)
}

// MarshalAppend packs the DirBlock and appends it onto the given
// byte slice for transport. It will create a new slice if buf is nil
// and grow the slice if necessary. However, if buf's capacity is large
// enough, MarshalAppend will do no allocation. If it does allocate,
// the returned slice will point to different storage than does the
// input argument, as with the built-in append function.
func (d *DirBlock) MarshalAppend(b []byte) ([]byte, error) {
	acc := accumulator{buf: b}
	return acc.DirBlock(d)
}

func (acc *accumulator) DirBlock(d *DirBlock) ([]byte, error) {
	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count n followed by n bytes.
	acc.byte(byte(d.Location.Endpoint.Transport))
	acc.string(string(d.Location.Endpoint.NetAddr))
	// Location.Reference: count n followed by n bytes.
	acc.string(string(d.Location.Reference))

	acc.int64(d.Offset)
	// Safety check.
	if d.Size > MaxBlockSize {
		return nil, ErrTooLarge
	}
	acc.int64(d.Size)
	acc.bytes(d.Packdata)

	return acc.result()
}

// Unmarshal unpacks the byte slice to recover the encoded DirBlock.
func (d *DirBlock) Unmarshal(b []byte) ([]byte, error) {
	cons := &consumer{buf: b}
	return cons.DirBlock(d)
}

// DirBlock unmarshals a DirBlock.
func (cons *consumer) DirBlock(d *DirBlock) ([]byte, error) {
	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count n followed by n bytes.
	d.Location.Endpoint.Transport = Transport(cons.byte())
	bytes := cons.bytes()
	d.Location.Endpoint.NetAddr = NetAddr(bytes)

	// d.Location.Reference
	//	Packing: 1 byte.
	//	Key: count h followed by h bytes.
	d.Location.Reference = Reference(cons.bytes())

	d.Offset = cons.int64()
	d.Size = cons.int64()

	// Packdata.
	if bytes = cons.bytes(); len(bytes) > 0 {
		// Must copy Packdata - can't return buffer's own contents.
		// (All the other slices are turned into strings, so are intrinsically copied.)
		d.Packdata = append([]byte(nil), bytes...)
	} else {
		d.Packdata = nil
	}

	return cons.remainder()
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
	acc := accumulator{buf: b}

	acc.string(string(d.SignedName))
	acc.byte(byte(d.Packing))
	acc.int64(int64(d.Time))

	// Blocks.
	// First a varint count, then the data.
	if uint64(len(d.Blocks)) > maxDirBlocks {
		return nil, ErrTooLarge
	}
	acc.int64(int64(len(d.Blocks)))
	for i := range d.Blocks {
		acc.DirBlock(&d.Blocks[i])
	}

	acc.bytes(d.Packdata)
	acc.string(string(d.Link))
	acc.string(string(d.Writer))

	// Name: if different from SignedName, count n followed by n bytes.
	// Otherwise, count zero with no bytes following.
	if d.Name != d.SignedName {
		acc.string(string(d.Name))
	} else {
		// Encode a special -1 that denotes Name == SignedName.
		acc.int64(-1)
	}

	acc.byte(byte(d.Attr))
	acc.int64(d.Sequence)

	return acc.result()
}

// ErrTooShort is returned by Unmarshal methods if the data is incomplete.
var ErrTooShort = errors.New("Unmarshal buffer too short")

// ErrTooLarge reports that an item is too large to be marshaled for transport to a
// potentially smaller machine. The limit is becase len(x) is of type int, which
// can be as small as 32 bits.
var ErrTooLarge = errors.New("data item too large")

// Unmarshal unpacks a marshaled DirEntry and stores it in the receiver.
// If successful, every field of d will be overwritten and the remaining
// data will be returned.
func (d *DirEntry) Unmarshal(b []byte) ([]byte, error) {
	cons := consumer{buf: b}
	// SignedName: count N followed by N bytes.
	bytes := cons.bytes()
	d.SignedName = PathName(bytes)

	// Packing: One byte.
	d.Packing = Packing(cons.byte())

	// Time.
	d.Time = Time(cons.int64())

	// Blocks. First a varint count, then the blocks.
	nBlocks := cons.int64()
	d.Blocks = nil
	switch {
	case nBlocks > maxDirBlocks:
		return nil, fmt.Errorf("block count out of range (max %d): %d", maxDirBlocks, nBlocks)
	case nBlocks < 0:
		return nil, fmt.Errorf("negative block count: %d", nBlocks)
	case nBlocks > 0:
		d.Blocks = make([]DirBlock, nBlocks)
		for i := range d.Blocks {
			cons.DirBlock(&d.Blocks[i])
		}
	}

	// Packdata.
	if bytes = cons.bytes(); len(bytes) > 0 {
		// Must copy Packdata - can't return buffer's own contents.
		// (All the other slices are turned into strings, so are intrinsically copied.)
		d.Packdata = append([]byte(nil), bytes...)
	} else {
		d.Packdata = nil
	}

	// Link: count N followed by N bytes.
	d.Link = PathName(cons.bytes())

	// Writer.
	d.Writer = UserName(cons.bytes())

	// Name.
	// If N is -1 Name equals SignedName.
	length64 := cons.int64()
	if length64 > MaxBlockSize {
		return nil, ErrTooLarge
	}
	length := int(length64)
	switch {
	case length == -1:
		// -1 is a special code that indicates Name == SignedName
		d.Name = d.SignedName
	case length < 0:
		return nil, fmt.Errorf("DirEntry has bad Name length: %d", length)
	default:
		d.Name = PathName(cons.nBytes(length))
	}

	d.Attr = Attribute(cons.byte())
	d.Sequence = cons.int64()

	return cons.remainder()
}

// String returns a default string representation of the time,
// in the format similar to RFC 3339: "2006-01-02T15:04:05 UTC"
// The time zone is always UTC.
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
