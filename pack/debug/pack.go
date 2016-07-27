// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package debugpack contains a trivial implementation of the Packer interface useful in tests.
// It encrypts the data with a randomly-chosen byte that is recorded in the Packdata.
// It does a trivial digital signature of the data and stores that in the Packdata as well.
// It claims the upspin.DebugPack Packing code.
package debugpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/rand"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/path"
	"upspin.io/upspin"
)

type testPack struct{}

var _ upspin.Packer = testPack{}

func init() {
	pack.Register(testPack{})
}

var (
	errTooShort    = errors.Str("destination slice too short")
	errBadPackdata = errors.Str("bad packdata")
)

func (testPack) Packing() upspin.Packing {
	return upspin.DebugPack
}

func (testPack) String() string {
	return "debug"
}

func (testPack) ReaderHashes(packdata []byte) ([][]byte, error) {
	return nil, nil
}

func (testPack) Share(context upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {
	// Nothing to do.
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

// Metadata is {DebugPack, cryptByte, signatureByte, N, path[N]}.
// The next two functions update the metadata's Packdata.

// TODO(adg): drop DebugPack from the packdata, it's now in DirEntry.Packing.

func cryptByte(d *upspin.DirEntry, packing bool) (byte, error) {
	switch len(d.Packdata) {
	case 0:
		return 0, errBadPackdata
	case 1:
		if !packing {
			// cryptByte must be present to unpack.
			return 0, errBadPackdata
		}
		// Add the crypt byte to the Packdata.
		cb := byte(rand.Int31())
		d.Packdata = append(d.Packdata, cb)
		return d.Packdata[1], nil
	default:
		return d.Packdata[1], nil
	}
}

func addSignature(d *upspin.DirEntry, signature byte) error {
	switch len(d.Packdata) {
	case 0, 1:
		return errBadPackdata
	case 2:
		d.Packdata = append(d.Packdata, signature)
		return nil
	default:
		d.Packdata[2] = signature
		return nil
	}
}

func (p testPack) Pack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const Pack = "Pack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(Pack, errors.Invalid, d.Name, err)
	}
	if len(d.Name) > 64*1024 {
		return nil, errors.E(Pack, errors.Invalid, d.Name, errors.Str("name too long"))
	}
	cb, err := cryptByte(d, true)
	if err != nil {
		return nil, errors.E(Pack, errors.Invalid, d.Name, err)
	}
	return &blockPacker{
		ctx:       ctx,
		entry:     d,
		cryptByte: cb,
	}, nil
}

type blockPacker struct {
	ctx       upspin.Context
	entry     *upspin.DirEntry
	cryptByte byte

	buf internal.LazyBuffer
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	const Pack = "Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	if len(cleartext) > 1024*1024*1024 {
		return nil, errors.E(Pack, errors.Invalid, bp.entry.Name, errors.Str("cleartext too long"))
	}

	// (re-)allocate shared buffer if necessary.
	ciphertext = bp.buf.Bytes(len(cleartext))

	crypt(bp.cryptByte, ciphertext, cleartext)

	// Compute size, offset, and checksum.
	size := int64(len(ciphertext))
	offs, err := bp.entry.Size()
	if err != nil {
		return nil, errors.E("Pack", errors.Invalid, err)
	}
	b := sha256.Sum256(ciphertext)
	sum := b[:]

	// Create and append new DirBlock record.
	block := upspin.DirBlock{
		Size:     size,
		Offset:   offs,
		Packdata: sum,
	}
	bp.entry.Blocks = append(bp.entry.Blocks, block)

	return ciphertext, nil
}

func crypt(b byte, out, in []byte) {
	if len(out) != len(in) {
		panic("input and output slice of different lengths")
	}
	for i, c := range in {
		out[i] = byte(c) ^ b
	}
}

func (bp *blockPacker) SetLocation(l upspin.Location) {
	bs := bp.entry.Blocks
	bs[len(bs)-1].Location = l
}

func (bp *blockPacker) Close() error {
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}
	putPath(bp.entry)
	addSignature(bp.entry, sign(bp.ctx, internal.BlockSum(bp.entry.Blocks), bp.entry.Name))
	return nil
}

func (p testPack) Unpack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const Unpack = "Unpack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(Unpack, errors.Invalid, d.Name, err)
	}
	cb, err := cryptByte(d, false)
	if err != nil {
		return nil, errors.E(Unpack, errors.Invalid, d.Name, err)
	}
	return &blockUnpacker{
		ctx:       ctx,
		entry:     d,
		cryptByte: cb,
		block:     -1,
	}, nil
}

type blockUnpacker struct {
	ctx       upspin.Context
	entry     *upspin.DirEntry
	block     int // index into entry.Blocks
	cryptByte byte

	buf internal.LazyBuffer
}

func (bp *blockUnpacker) NextBlock() (upspin.DirBlock, bool) {
	bp.block++
	bs := bp.entry.Blocks
	if bp.block >= len(bs) {
		return upspin.DirBlock{}, false
	}
	b := bs[bp.block]
	return b, true
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	const Unpack = "Unpack"

	if len(ciphertext) > 64*1024+1024*1024*1024 {
		return nil, errors.E(Unpack, errors.Invalid, bp.entry.Name, errors.Str("ciphertext too long"))
	}

	// Validate checksum.
	b := sha256.Sum256(ciphertext)
	sum := b[:]
	if got, want := sum, bp.entry.Blocks[bp.block].Packdata; !bytes.Equal(got, want) {
		return nil, errors.E("Unpack", bp.entry.Name, errors.Str("checksum mismatch"))
	}

	cleartext = bp.buf.Bytes(len(ciphertext))

	crypt(bp.cryptByte, cleartext, ciphertext)

	return cleartext, nil
}

func (p testPack) PackLen(context upspin.Context, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, d); err != nil {
		return -1
	}
	// Add packing to packmeta if not already there
	if d != nil && len(d.Packdata) == 0 {
		d.Packdata = []byte{byte(upspin.DebugPack)}
	}
	_, err := cryptByte(d, true)
	if err != nil {
		return -1
	}
	return len(cleartext)
}

func (p testPack) UnpackLen(context upspin.Context, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, d); err != nil {
		return -1
	}
	return len(ciphertext)
}

func sign(ctx upspin.Context, data []byte, name upspin.PathName) byte {
	key, err := getKey(ctx, name)
	if err != nil {
		panic(err)
	}
	signature := byte(0)
	for i, c := range data {
		signature ^= c ^ key[i%len(key)]
	}
	for i, c := range []byte(name) {
		signature ^= c ^ key[i%len(key)]
	}
	return signature
}

// Name implements upspin.Pack.Name.
func (testPack) Name(ctx upspin.Context, d *upspin.DirEntry, newName upspin.PathName) error {
	const Name = "Name"
	if d.IsDir() {
		return errors.E(Name, errors.IsDir, d.Name, "cannot rename directory")
	}
	parsed, err := path.Parse(newName)
	if err != nil {
		return errors.E(Name, err)
	}

	// Update directory entry and metadata with new name.
	name := parsed.Path()
	d.Name = name
	oldName, err := getPath(d)
	if err != nil {
		return errors.E(Name, errors.Invalid, d.Name, err)
	}
	putPath(d)

	// Remove old name from signature.
	signature := d.Packdata[2]
	key, err := getKey(ctx, oldName)
	if err != nil {
		panic(err)
	}
	for i, c := range []byte(oldName) {
		signature ^= c ^ key[i%len(key)]
	}

	// Add new name to signature. The key may also be different since this
	// may be a different user.
	key, err = getKey(ctx, name)
	for i, c := range []byte(name) {
		signature ^= c ^ key[i%len(key)]
	}
	d.Packdata[2] = signature

	return nil
}

// getKey returns the user key for the user in name.
func getKey(ctx upspin.Context, name upspin.PathName) (upspin.PublicKey, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return "", err
	}
	user, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		return "", err
	}
	u, err := user.Lookup(parsed.User())
	if err != nil {
		return "", err
	}
	if u.PublicKey == "" {
		return "", errors.Str("no key for signing")
	}
	return u.PublicKey, nil
}

// putPath adds (or replaces) the path in the packdata.
func putPath(d *upspin.DirEntry) {
	d.Packdata = d.Packdata[:3]
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(d.Name)))
	d.Packdata = append(d.Packdata, buf[:n]...)
	d.Packdata = append(d.Packdata, d.Name...)
}

// getPath returns the path from the packdata.
func getPath(d *upspin.DirEntry) (upspin.PathName, error) {
	if len(d.Packdata) < 4 {
		return "", errBadPackdata
	}
	m, n := binary.Uvarint(d.Packdata[3:])
	if n < 0 {
		return "", errBadPackdata
	}
	buf := d.Packdata[3+int(n):]
	if len(buf) != int(m) {
		return "", errBadPackdata
	}
	return upspin.PathName(buf), nil
}
