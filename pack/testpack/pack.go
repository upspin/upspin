// Package testpack contains a trivial implementation of the Packer interface useful in tests.
// It claims the upspin.DebugPack Packing code.
// TODO: Not concurrency-safe.
package testpack

import (
	"encoding/binary"
	"errors"

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
	// Temporarily overwrites ciphertext. We restore it but: TODO.
	defer crypt(ciphertext)
	crypt(ciphertext)
	nameLen, n := binary.Uvarint(ciphertext)
	if n == 0 {
		return "", 0, errTooShort
	}
	if n < 0 || nameLen > 64*1024 || n+int(nameLen) > len(ciphertext) {
		return "", 0, errors.New("namelen overflow") // TODO
	}
	boundary := n + int(nameLen) // Between name and payload.
	name, payload := ciphertext[n:boundary], ciphertext[boundary:]
	if len(payload) > len(cleartext) {
		return "", 0, errTooShort
	}
	copy(cleartext, payload)
	return upspin.PathName(name), len(payload), nil
}

func (testPack) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(name)))
	return n + len(name) + len(cleartext)
}

func (testPack) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	// Temporarily overwrites ciphertext. We restore it but: TODO.
	defer crypt(ciphertext)
	crypt(ciphertext)
	nameLen, n := binary.Uvarint(ciphertext)
	if n <= 0 || nameLen > 64*1024 {
		return -1
	}
	return len(ciphertext) - (n + int(nameLen))
}

func (testPack) String() string {
	return "test"
}
