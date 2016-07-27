// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package plain is the no-op Packing that passes the data untouched.
// Metadata is not affected. The path name is not stored in the packed data.
package plain

import (
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

type plainPack struct{}

var _ upspin.Packer = plainPack{}

func init() {
	pack.Register(plainPack{})
}

var errTooShort = errors.Str("destination slice too short")

func (plainPack) Packing() upspin.Packing {
	return upspin.PlainPack
}

func (plainPack) String() string {
	return "plain"
}

func (plainPack) ReaderHashes(packdata []byte) ([][]byte, error) {
	return nil, nil
}

func (plainPack) Share(context upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {
	// Nothing to do.
}

func (p plainPack) Pack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E("Pack", errors.Invalid, d.Name, err)
	}
	return &blockPacker{
		ctx:   ctx,
		entry: d,
	}, nil
}

type blockPacker struct {
	ctx   upspin.Context
	entry *upspin.DirEntry
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	ciphertext = cleartext

	size := int64(len(ciphertext))
	offs := int64(0)
	if bs := bp.entry.Blocks; len(bs) > 0 {
		offs = bs[len(bs)-1].Offset + size
	}

	block := upspin.DirBlock{
		Size:   size,
		Offset: offs,
	}
	bp.entry.Blocks = append(bp.entry.Blocks, block)

	return
}

func (bp *blockPacker) SetLocation(l upspin.Location) {
	bs := bp.entry.Blocks
	bs[len(bs)-1].Location = l
}

func (bp *blockPacker) Close() error {
	return nil
}

func (p plainPack) Unpack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const Unpack = "Unpack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(Unpack, errors.Invalid, d.Name, err)
	}
	return &blockUnpacker{
		ctx:          ctx,
		entry:        d,
		BlockTracker: internal.NewBlockTracker(d.Blocks),
	}, nil
}

type blockUnpacker struct {
	ctx                   upspin.Context
	entry                 *upspin.DirEntry
	internal.BlockTracker // provides NextBlock method and Block field
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	cleartext = ciphertext
	return
}

// Name implements upspin.Name.
func (p plainPack) Name(ctx upspin.Context, dirEntry *upspin.DirEntry, newName upspin.PathName) error {
	const Name = "Name"
	if dirEntry.IsDir() {
		return errors.E(Name, errors.IsDir, dirEntry.Name, "cannot rename directory")
	}
	parsed, err := path.Parse(newName)
	if err != nil {
		return errors.E(Name, err)
	}
	dirEntry.Name = parsed.Path()
	return nil
}

func (p plainPack) PackLen(context upspin.Context, cleartext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(cleartext)
}

func (p plainPack) UnpackLen(context upspin.Context, ciphertext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(ciphertext)
}
