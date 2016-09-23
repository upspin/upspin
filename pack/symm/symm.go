// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package symm implements an AES-GCM symmetric encryption packer.
package symm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"

	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/upspin"
)

// Given the problems of secure distribution of symmetric keys across
// multiple users, this package is primarily designed for a single
// user, such as a directory server encrypting directory entries
// before placing them in a less trusted storage server.

var _ upspin.Packer = symm{}

type symm struct{}

const (
	aesKeyLen            = 32 // AES-256
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

func init() {
	pack.Register(symm{})
}

var (
	errKeyLength = errors.Str("wrong key length for AES-256")
	errVerify    = errors.Str("does not verify")
	aead         cipher.AEAD
)

func (symm symm) Packing() upspin.Packing {
	return upspin.SymmPack
}

func (symm symm) PackLen(ctx upspin.Context, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(symm, d); err != nil {
		return -1
	}
	return len(cleartext) + aead.Overhead()
}

func (symm symm) UnpackLen(ctx upspin.Context, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(symm, d); err != nil {
		return -1
	}
	return len(ciphertext) - aead.Overhead()
}

func (symm symm) String() string {
	return "symm"
}

func (symm symm) Pack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const op = "pack/symm.Pack"
	if err := pack.CheckPacking(symm, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}

	// TODO(adg): support append; for now assume a new file.
	d.Blocks = nil

	bp, err := newBlockPacker(op, ctx, d)
	return bp, err
}

type blockPacker struct {
	ctx   upspin.Context
	entry *upspin.DirEntry
	aead  cipher.AEAD
	buf   internal.LazyBuffer
}

func newBlockPacker(op string, ctx upspin.Context, d *upspin.DirEntry) (*blockPacker, error) {
	bp := &blockPacker{
		ctx:   ctx,
		entry: d,
	}
	// Fetch the symmetric key.
	dkey, err := factotum.SymmSecret()
	if err != nil {
		return bp, errors.E(op, err)
	}
	if len(dkey) != aesKeyLen {
		return bp, errors.E(op, errKeyLength)
	}

	// Set up the stream cipher.
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return bp, errors.E(op, err)
	}
	aead, err = cipher.NewGCM(block)
	if err != nil {
		return bp, errors.E(op, err)
	}

	bp.aead = aead
	return bp, nil
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	const op = "pack/symm.blockPacker.Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	ciphertext = bp.buf.Bytes(len(cleartext) + aead.Overhead())

	// Pick fresh nonce for this block.
	nonce := make([]byte, aead.NonceSize())
	_, err = rand.Read(nonce)
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	// Encrypt.
	additionalData := []byte{}
	ciphertext = aead.Seal(ciphertext[:0], nonce, cleartext, additionalData)

	// Compute size, offset, and checksum.
	size := int64(len(ciphertext))
	offs, err := bp.entry.Size()
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	// Create and append new DirBlock record.
	block := upspin.DirBlock{
		Size:     size,
		Offset:   offs,
		Packdata: nonce,
	}
	bp.entry.Blocks = append(bp.entry.Blocks, block)

	return ciphertext, nil
}

func (bp *blockPacker) SetLocation(l upspin.Location) {
	bs := bp.entry.Blocks
	bs[len(bs)-1].Location = l
}

func (bp *blockPacker) Close() error {
	return nil
}

func (symm symm) Unpack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op = "pack/symm.Unpack"
	bp, err := newBlockPacker(op, ctx, d)
	if err != nil {
		return nil, err
	}
	aead = bp.aead

	// Call Size to check that the block Offsets and Sizes are consistent.
	if _, err := d.Size(); err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	return &blockUnpacker{
		ctx:          ctx,
		entry:        d,
		BlockTracker: internal.NewBlockTracker(d.Blocks),
		aead:         bp.aead,
	}, nil
}

type blockUnpacker struct {
	ctx                   upspin.Context
	entry                 *upspin.DirEntry
	internal.BlockTracker // provides NextBlock method and Block field
	aead                  cipher.AEAD

	buf internal.LazyBuffer
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	const op = "pack/symm.blockUpacker.Unpack"

	cleartext = bp.buf.Bytes(len(ciphertext) - aead.Overhead())

	// Decrypt.
	nonce := bp.entry.Blocks[bp.Block].Packdata
	additionalData := []byte{}
	return aead.Open(cleartext[:0], nonce, ciphertext, additionalData)
}

// Methods that are not implemented.

var errNotImplemented = errors.Str("not implemented")

func (symm symm) Name(ctx upspin.Context, d *upspin.DirEntry, newName upspin.PathName) error {
	const op = "pack/symm.Name"
	return errors.E(op, errNotImplemented)
}

func (symm symm) ReaderHashes(packdata []byte) (readers [][]byte, err error) {
	const op = "pack/symm.ReaderHashes"
	return nil, errors.E(op, errNotImplemented)
}

func (symm symm) Share(ctx upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {
}
