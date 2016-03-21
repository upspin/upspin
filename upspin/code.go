package upspin

import (
	"encoding/binary"
	"errors"
)

// This file contains implementations of things like marshaling of the
// basic Upspin types.

// Marshal packs the Location into a byte slice for transport.
func (Location) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Location.
func (Location) Unmarshal([]byte) error {
	panic("unimplemented")
}

// Marshal packs the Reference into a byte slice for transport.
func (Reference) Marshal([]byte) error {
	panic("unimplemented")
}

// Unmarshal unpacks the byte slice to recover the encoded Reference.
func (Reference) Unmarshal([]byte) error {
	panic("unimplemented")
}

// Packing returns the Packing type to which this PackData applies, identified
// by the initial byte of the PackData.
func (p PackData) Packing() Packing {
	return Packing(p[0])
}

// Data returns the data in the PackData, the bytes after the initial Packing metadata byte.
func (p PackData) Data() []byte {
	return p[1:]
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
// input argument.
func (d *DirEntry) MarshalAppend(b []byte) ([]byte, error) {
	var tmp [16]byte // For use by PutVarint and PutUvarint.
	// Name: count N followed by N bytes.
	b = appendString(b, string(d.Name))

	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count N followed by N bytes.
	b = append(b, byte(d.Location.Endpoint.Transport))
	b = appendString(b, string(d.Location.Endpoint.NetAddr))
	// Location.Reference:
	//	Packing: 1 byte.
	//	Key: count N followed by N bytes.
	b = append(b, byte(d.Location.Reference.Packing))
	b = appendString(b, d.Location.Reference.Key)

	// Metadata.
	//	IsDir: 1 byte (0 false, 1 true)
	//	Sequence: varint encoded.
	//	PackData: count N, followed by N bytes
	//	Readers: count N followed by N*(count N, followed by N bytes)
	if d.Metadata.IsDir {
		b = append(b, byte(1))
	} else {
		b = append(b, byte(0))
	}
	N := binary.PutVarint(tmp[:], d.Metadata.Sequence)
	b = append(b, tmp[:N]...)
	b = appendBytes(b, d.Metadata.PackData)
	N = binary.PutUvarint(tmp[:], uint64(len(d.Metadata.Readers)))
	b = append(b, tmp[:N]...)
	for _, r := range d.Metadata.Readers {
		b = appendString(b, string(r))
	}
	return b, nil
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutUvarint.
	N := binary.PutUvarint(tmp[:], uint64(len(str)))
	b = append(b, tmp[:N]...)
	b = append(b, str...)
	return b
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutUvarint.
	N := binary.PutUvarint(tmp[:], uint64(len(bytes)))
	b = append(b, tmp[:N]...)
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
	if b == nil {
		return nil, ErrTooShort
	}
	d.Name = PathName(bytes)

	// Location:
	// Location.Endpoint:
	//	Transport: 1 byte.
	//	NetAddr: count N followed by N bytes.
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Location.Endpoint.Transport = Transport(b[0])
	b = b[1:]
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Location.Endpoint.NetAddr = NetAddr(bytes)

	// Location.Reference:
	//	Packing: 1 byte.
	//	Key: count N followed by N bytes.
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Location.Reference.Packing = Packing(b[0])
	b = b[1:]
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	d.Location.Reference.Key = string(bytes)

	// Metadata.
	//	IsDir: 1 byte (0 false, 1 true)
	//	Sequence: varint encoded.
	//	PackData: count N, followed by N bytes
	//	Readers: count N followed by N*(count N, followed by N bytes)
	if len(b) < 1 {
		return nil, ErrTooShort
	}
	d.Metadata.IsDir = b[0] != 0
	b = b[1:]
	seq, N := binary.Varint(b)
	if N == 0 {
		return nil, ErrTooShort
	}
	d.Metadata.Sequence = seq
	b = b[N:]
	bytes, b = getBytes(b)
	if b == nil {
		return nil, ErrTooShort
	}
	// Must copy packdata - can't return buffer's own contents.
	// (All the other slices are turned into strings, so are intrinsically copied.)
	d.Metadata.PackData = make(PackData, len(bytes))
	copy(d.Metadata.PackData, bytes)
	u, N := binary.Uvarint(b)
	if N == 0 {
		return nil, ErrTooShort
	}
	d.Metadata.Readers = make([]UserName, u)
	b = b[N:]
	for i := range d.Metadata.Readers {
		bytes, b = getBytes(b)
		if b == nil {
			return nil, ErrTooShort
		}
		d.Metadata.Readers[i] = UserName(bytes)
	}
	return b, nil
}

// getBytes unmarshals the byte slice at b (uvarint count followed by bytes)
// and returns the slice followed by the remaining bytes.
// If there is insufficient data, both return values will be nil.
func getBytes(b []byte) (data, remaining []byte) {
	u, N := binary.Uvarint(b)
	if N == 0 || len(b) < N+int(u) {
		return nil, nil
	}
	return b[N : N+int(u)], b[N+int(u):]
}
