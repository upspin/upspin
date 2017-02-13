// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package plain is a simple Packing that passes the data untouched.
// The pathname, packing, and time are signed.
package plain

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/path"
	"upspin.io/upspin"
)

type plainPack struct{}

var _ upspin.Packer = plainPack{}

func init() {
	pack.Register(plainPack{})
}

const (
	aesKeyLen     = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	marshalBufLen = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
)

var (
	errVerify           = errors.Str("does not verify")
	errWriter           = errors.Str("empty Writer in Metadata")
	errSignedNameNotSet = errors.Str("empty SignedName")
	sig0                upspin.Signature // for returning error of correct type
)

func (plainPack) Packing() upspin.Packing {
	return upspin.PlainPack
}

func (plainPack) String() string {
	return "plain"
}

func (plainPack) ReaderHashes(packdata []byte) ([][]byte, error) {
	return nil, nil
}

func (plainPack) Share(cfg upspin.Config, readers []upspin.PublicKey, packdata []*[]byte) {
	// Nothing to do.
}

func (p plainPack) Pack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const op = "pack/plain.Pack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}
	if len(d.SignedName) == 0 {
		return nil, errors.E(op, errors.Invalid, d.Name, errSignedNameNotSet)
	}

	return &blockPacker{
		cfg:   cfg,
		entry: d,
	}, nil
}

type blockPacker struct {
	cfg   upspin.Config
	entry *upspin.DirEntry
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	const op = "pack/plain.blockPacker.Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	ciphertext = cleartext

	size := int64(len(ciphertext))
	offs, err := bp.entry.Size()
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
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

// Close implements upspin.BlockPacker.
func (bp *blockPacker) Close() error {
	const op = "pack/plain.blockPacker.Close"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}

	name := bp.entry.SignedName
	cfg := bp.cfg

	// Compute entry signature dkey=sum=0.
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	sig, err := cfg.Factotum().FileSign(name, bp.entry.Time, dkey, sum)
	if err != nil {
		return errors.E(op, err)
	}

	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{})
}

func (p plainPack) Unpack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op = "pack/plain.Unpack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}
	// Call Size to check that the block Offsets and Sizes are consistent.
	if _, err := d.Size(); err != nil {
		return nil, errors.E(op, d.Name, err)
	}
	return &blockUnpacker{
		cfg:          cfg,
		entry:        d,
		BlockTracker: internal.NewBlockTracker(d.Blocks),
	}, nil
}

type blockUnpacker struct {
	cfg                   upspin.Config
	entry                 *upspin.DirEntry
	internal.BlockTracker // provides NextBlock method and Block field
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	cleartext = ciphertext
	return
}

func (bp *blockUnpacker) Close() error {
	return nil
}

// Name implements upspin.Name.
func (p plainPack) Name(cfg upspin.Config, dirEntry *upspin.DirEntry, newName upspin.PathName) error {
	const op = "pack/plain.Name"
	if dirEntry.IsDir() {
		return errors.E(op, errors.IsDir, dirEntry.Name, "cannot rename directory")
	}
	parsed, err := path.Parse(newName)
	if err != nil {
		return errors.E(op, err)
	}
	dirEntry.Name = parsed.Path()
	dirEntry.SignedName = dirEntry.Name
	return nil
}

func (p plainPack) PackLen(cfg upspin.Config, cleartext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(cleartext)
}

func (p plainPack) UnpackLen(cfg upspin.Config, ciphertext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(ciphertext)
}

func pdMarshal(dst *[]byte, sig, sig2 upspin.Signature) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation
	n := packdataLen()
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	n = 0
	n += internal.PutBytes((*dst)[n:], sig.R.Bytes())
	n += internal.PutBytes((*dst)[n:], sig.S.Bytes())
	if sig2.R == nil {
		zero := big.NewInt(0)
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += internal.PutBytes((*dst)[n:], sig2.R.Bytes())
	n += internal.PutBytes((*dst)[n:], sig2.S.Bytes())
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

func pdUnmarshal(pd []byte) (sig, sig2 upspin.Signature, err error) {
	if len(pd) == 0 {
		return sig0, sig0, errors.Str("nil packdata")
	}
	n := 0
	sig.R = big.NewInt(0)
	sig.S = big.NewInt(0)
	sig2.R = big.NewInt(0)
	sig2.S = big.NewInt(0)
	buf := make([]byte, marshalBufLen)
	n += internal.GetBytes(&buf, pd[n:])
	sig.R.SetBytes(buf)
	n += internal.GetBytes(&buf, pd[n:])
	sig.S.SetBytes(buf)
	n += internal.GetBytes(&buf, pd[n:])
	sig2.R.SetBytes(buf)
	n += internal.GetBytes(&buf, pd[n:])
	sig2.S.SetBytes(buf)
	return sig, sig2, nil
}

// packdataLen returns n big enough for packing, sig.R, sig.S
func packdataLen() int {
	return 2*marshalBufLen + binary.MaxVarintLen64 + 1
}
