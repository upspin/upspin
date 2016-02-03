// Package testpack contains a trivial implementation of the Packer interface useful in tests.
// It claims the upspin.DebugPack Packing code.
// TODO: Not concurrency-safe.
package testpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

type testPack struct{}

var _ upspin.Packer = testPack{}

func init() {
	pack.Register(testPack{})
}

var errTooShort = errors.New("TestPack: destination slice too short")

func (testPack) Packing() upspin.Packing {
	return upspin.Debug
}

// Lazy reversible encryption/decryption. Simple; fine for tests.
func crypt(data []byte) {
	for i, c := range data {
		data[i] = c ^ 0x55
	}
}

// cryptByteReader wraps a bytes.Reader and encrypts/decrypts the bytes its reads by xoring with 0x55.
type cryptByteReader struct {
	br *bytes.Reader
}

func (cr cryptByteReader) ReadByte() (byte, error) {
	c, err := cr.br.ReadByte()
	return c ^ 0x55, err
}

// Message is {N, path[N], data}. N is unsigned varint-encoded.

func (testPack) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(name) > 64*1024 {
		return 0, errors.New("name too long")
	}
	if len(cleartext) > 1024*1024*1024 {
		return 0, errors.New("cleartext too long")
	}
	if len(ciphertext) <= 4 {
		return 0, errTooShort
	}
	// Simple: Append to ciphertext and complain at the end if an allocation has happened.
	// Constrain the allocation through a slice with cap==len(ciphertext).
	// Thus it allocates only when there's an overflow. Silly but easy. Fine for tests.
	out := ciphertext[0:0:len(ciphertext)]
	capacity := cap(out)
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(name)))
	out = append(out, buf[:n]...)
	out = append(out, name...)
	out = append(out, cleartext...)
	if cap(out) != capacity {
		// Allocation occurred.
		return 0, errTooShort
	}
	crypt(out)
	return len(out), nil
}

func (testPack) Unpack(cleartext, ciphertext []byte, meta *upspin.Metadata) (upspin.PathName, int, error) {
	if len(ciphertext) > 64*1024+1024*1024*1024 {
		return "", 0, errors.New("crazy length") // TODO
	}
	br := bytes.NewReader(ciphertext)
	cr := cryptByteReader{br}
	nameLen, err := binary.ReadUvarint(cr)
	n, _ := br.Seek(0, 1) // Number of bytes consumed reading nameLen.
	if err != nil || nameLen > 64*1024 || int(n)+int(nameLen) > len(ciphertext) {
		return "", 0, errors.New("namelen overflow") // TODO
	}
	nameBuf := make([]byte, nameLen)
	for i := 0; i < int(nameLen); i++ {
		nameBuf[i], _ = cr.ReadByte()
	}
	var i int
	for i = 0; ; i++ {
		c, err := cr.ReadByte()
		if err == io.EOF {
			break
		}
		if i >= len(cleartext) {
			return "", 0, errTooShort
		}
		cleartext[i] = c
	}
	return upspin.PathName(nameBuf), i, nil
}

func (testPack) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(name)))
	return n + len(name) + len(cleartext)
}

func (testPack) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	br := bytes.NewReader(ciphertext)
	cr := cryptByteReader{br}
	nameLen, err := binary.ReadUvarint(cr)
	if err != nil {
		return -1
	}
	n, err := br.Seek(0, 1) // Number of bytes consumed reading nameLen.
	if err != nil || nameLen > 64*1024 || int(n)+int(nameLen) > len(ciphertext) {
		return -1
	}
	br.Seek(int64(nameLen), 1)
	return int(br.Len())
}

func (testPack) String() string {
	return "test"
}
