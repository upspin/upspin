// Package debugpack contains a trivial implementation of the Packer interface useful in tests.
// It encrypts the data with a randomly-chosen byte that is recorded in the PackData.
// It does a trivial digital signature of the data and stores that in the PackData as well.
// It claims the upspin.DebugPack Packing code.
package debugpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

type testPack struct{}

var _ upspin.Packer = testPack{}

func init() {
	pack.Register(testPack{})
}

var (
	errTooShort     = errors.New("TestPack: destination slice too short")
	errBadMetadata  = errors.New("bad metadata")
	errBadSignature = errors.New("signature validation failed")
	errNoKey        = errors.New("no key for signature")
)

func (testPack) Packing() upspin.Packing {
	return upspin.DebugPack
}

func (testPack) String() string {
	return "debug"
}

// cryptByteReader wraps a bytes.Reader and encrypts/decrypts the bytes its reads by xoring with cryptByte.
type cryptByteReader struct {
	crypt byte
	br    *bytes.Reader
}

func (cr cryptByteReader) ReadByte() (byte, error) {
	c, err := cr.br.ReadByte()
	return c ^ cr.crypt, err
}

func cryptByte(meta *upspin.Metadata, packing bool) (byte, error) {
	switch len(meta.PackData) {
	case 1:
		if !packing {
			// cryptByte must be present to unpack.
			return 0, errBadMetadata
		}
		// Add the crypt byte to the PackData.
		cb := byte(rand.Int31())
		meta.PackData = append(meta.PackData, cb)
		fallthrough
	case 2, 3:
		return meta.PackData[1], nil
	default:
		return 0, errBadMetadata
	}
}

func addSignature(meta *upspin.Metadata, signature byte) error {
	switch len(meta.PackData) {
	case 2:
		meta.PackData = append(meta.PackData, signature)
		return nil
	case 3:
		meta.PackData[2] = signature
		return nil
	default:
		return errBadMetadata
	}
}

// Message is {N, path[N], data}. N is unsigned varint-encoded.
// Metadata is {DebugPack, cryptByte, signatureByte}.

func (p testPack) Pack(context *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return 0, err
	}
	if len(name) > 64*1024 {
		return 0, errors.New("name too long")
	}
	if len(cleartext) > 1024*1024*1024 {
		return 0, errors.New("cleartext too long")
	}
	if len(ciphertext) <= 4 {
		return 0, errTooShort
	}
	if len(context.PrivateKey.Private) == 0 {
		return 0, errNoKey
	}
	cb, err := cryptByte(meta, true)
	if err != nil {
		return 0, err
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
	addSignature(meta, sign(cleartext, context.PrivateKey.Private))
	for i, c := range out {
		out[i] = c ^ cb
	}
	return len(out), nil
}

func (p testPack) Unpack(context *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return 0, err
	}
	if len(ciphertext) > 64*1024+1024*1024*1024 {
		return 0, errors.New("testPack.Unpack: crazy length")
	}
	if len(context.PrivateKey.Private) == 0 {
		return 0, errNoKey
	}
	cb, err := cryptByte(meta, false)
	if err != nil {
		return 0, err
	}
	br := bytes.NewReader(ciphertext)
	cr := cryptByteReader{cb, br}
	nameLen, err := binary.ReadUvarint(cr)
	n, _ := br.Seek(0, 1) // Number of bytes consumed reading nameLen.
	if err != nil || nameLen > 64*1024 || int(n)+int(nameLen) > len(ciphertext) {
		return 0, errors.New("testPack.Unpack: namelen overflow")
	}
	if len(name) != int(nameLen) {
		// Work hard to get a good error message. This has caught bugs.
		var s []byte
		for i := 0; i < int(nameLen); i++ {
			c, _ := cr.ReadByte()
			if err != nil { // Cannot happen, really.
				break
			}
			s = append(s, c)
		}
		return 0, fmt.Errorf("testPack.Unpack: want %q; found %q\n", name, s)
	}
	for i := 0; i < int(nameLen); i++ {
		c, err := cr.ReadByte()
		if err != nil { // Cannot happen, really.
			return 0, err
		}
		if c != name[i] {
			return 0, errors.New("testPack.Unpack: name mismatch")
		}
	}
	var i int
	for i = 0; ; i++ {
		c, err := cr.ReadByte()
		if err == io.EOF {
			break
		}
		if i >= len(cleartext) {
			return 0, errTooShort
		}
		cleartext[i] = c
	}
	signature := sign(cleartext[:i], context.PrivateKey.Private)
	if len(meta.PackData) < 3 || signature != meta.PackData[2] {
		return 0, errBadSignature
	}
	return i, nil
}

func (p testPack) PackLen(context *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return -1
	}
	_, err := cryptByte(meta, true)
	if err != nil {
		return -1
	}
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(name)))
	return n + len(name) + len(cleartext)
}

func (p testPack) UnpackLen(context *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return -1
	}
	cb, err := cryptByte(meta, false)
	if err != nil {
		return -1
	}
	br := bytes.NewReader(ciphertext)
	cr := cryptByteReader{cb, br}
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

func sign(data, key []byte) byte {
	signature := byte(0)
	for i, c := range data {
		signature ^= c ^ key[i%len(key)]
	}
	return signature
}
