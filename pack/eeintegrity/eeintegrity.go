// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ei implements an elliptic-curve end-to-end integrity-checked packer.
package ei // import "upspin.io/pack/eeintegrity"

// This is a copy of pack/ee/ee.go, with the encryption removed.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/pack/packutil"
	"upspin.io/path"
	"upspin.io/upspin"
)

var _ upspin.Packer = ei{}

type ei struct{}

const (
	aesKeyLen     = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	marshalBufLen = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
)

func init() {
	pack.Register(ei{})
}

var (
	errVerify           = errors.Str("does not verify")
	errWriter           = errors.Str("empty Writer in Metadata")
	errSignedNameNotSet = errors.Str("empty SignedName")
	sig0                upspin.Signature // for returning error of correct type
)

// Packing implements upspin.Packer.
func (ei ei) Packing() upspin.Packing {
	return upspin.EEIntegrityPack
}

// PackLen implements upspin.Packer.
func (ei ei) PackLen(cfg upspin.Config, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ei, d); err != nil {
		return -1
	}
	return len(cleartext)
}

// UnpackLen implements upspin.Packer.
func (ei ei) UnpackLen(cfg upspin.Config, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ei, d); err != nil {
		return -1
	}
	return len(ciphertext)
}

// String implements upspin.Packer.
func (ei ei) String() string {
	return "eeintegrity"
}

// Pack implements upspin.Packer.
func (ei ei) Pack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const op errors.Op = "pack/eeintegrity.Pack"
	if err := pack.CheckPacking(ei, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}
	if len(d.SignedName) == 0 {
		return nil, errors.E(op, errors.Invalid, d.Name, errSignedNameNotSet)
	}

	// TODO(adg): support append; for now assume a new file.
	d.Blocks = nil

	return &blockPacker{
		cfg:   cfg,
		entry: d,
	}, nil
}

type blockPacker struct {
	cfg   upspin.Config
	entry *upspin.DirEntry

	buf internal.LazyBuffer
}

// Pack implements upspin.BlockPacker.
func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	const op errors.Op = "pack/eeintegrity.blockPacker.Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	ciphertext = bp.buf.Bytes(len(cleartext))
	copy(ciphertext, cleartext)

	// Compute size, offset, and checksum.
	size := int64(len(ciphertext))
	offs, err := bp.entry.Size()
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
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

// SetLocation implements upspin.BlockPacker.
func (bp *blockPacker) SetLocation(l upspin.Location) {
	bs := bp.entry.Blocks
	bs[len(bs)-1].Location = l
}

// Close implements upspin.BlockPacker.
func (bp *blockPacker) Close() error {
	const op errors.Op = "pack/eeintegrity.blockPacker.Close"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}

	// Compute checksum of block hashes.
	sum := internal.BlockSum(bp.entry.Blocks)

	// Compute entry signature with dkey=0.
	f := bp.cfg.Factotum()
	e := bp.entry
	dkey := make([]byte, aesKeyLen)
	sig, err := f.FileSign(f.DirEntryHash(e.SignedName, e.Link, e.Attr, e.Packing, e.Time, dkey, sum))
	if err != nil {
		return errors.E(op, err)
	}
	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{}, sum)
}

// Unpack implements upspin.Packer.
func (ei ei) Unpack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op errors.Op = "pack/eeintegrity.Unpack"
	if err := pack.CheckPacking(ei, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}

	// Call Size to check that the block Offsets and Sizes are consistent.
	if _, err := d.Size(); err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	sig, sig2, hash, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	// Check that our stored+signed block checksum matches the sum of the actual blocks.
	if got, want := internal.BlockSum(d.Blocks), hash; !bytes.Equal(got, want) {
		return nil, errors.E(op, d.Name, "checksum mismatch")
	}

	// Fetch writer public key.
	writer := d.Writer
	if len(writer) == 0 {
		return nil, errors.E(op, d.Name, errWriter)
	}
	writerRawPubKey, err := packutil.GetPublicKey(cfg, writer)
	if err != nil {
		return nil, errors.E(op, writer, err)
	}
	writerPubKey, err := factotum.ParsePublicKey(writerRawPubKey)
	if err != nil {
		return nil, errors.E(op, writer, err)
	}

	f := cfg.Factotum()
	dkey := make([]byte, aesKeyLen)
	// Verify that this was signed with the writer's old or new public key.
	vhash := f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, hash)
	if !ecdsa.Verify(writerPubKey, vhash, sig.R, sig.S) &&
		!ecdsa.Verify(writerPubKey, vhash, sig2.R, sig2.S) {
		// Check sig2 in case writerPubKey is rotating.
		return nil, errors.E(op, d.Name, writer, errVerify)
		// TODO(ehg) If reader is owner, consider trying even older factotum keys.
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

	buf internal.LazyBuffer
}

// Unpack implements upspin.BlockUnpacker.
func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	const op errors.Op = "pack/eeintegrity.blockUpacker.Unpack"
	// Validate checksum.
	b := sha256.Sum256(ciphertext)
	sum := b[:]
	if got, want := sum, bp.entry.Blocks[bp.Block].Packdata; !bytes.Equal(got, want) {
		return nil, errors.E(op, bp.entry.Name, "checksum mismatch")
	}

	cleartext = bp.buf.Bytes(len(ciphertext))
	copy(cleartext, ciphertext)

	return cleartext, nil
}

func (bp *blockUnpacker) Close() error {
	return nil
}

// ReaderHashes is unused in this packer.
func (ei ei) ReaderHashes(packdata []byte) (readers [][]byte, err error) {
	return
}

// Share is unused in this packer.
func (ei ei) Share(cfg upspin.Config, readers []upspin.PublicKey, packdata []*[]byte) {
}

// Name implements upspin.Name.
func (ei ei) Name(cfg upspin.Config, d *upspin.DirEntry, newName upspin.PathName) error {
	const op errors.Op = "pack/plain.Name"
	return ei.updateDirEntry(op, cfg, d, newName, d.Time)
}

// SetTime implements upspin.SetTime.
func (ei ei) SetTime(cfg upspin.Config, d *upspin.DirEntry, t upspin.Time) error {
	const op errors.Op = "pack/plain.SetTime"
	return ei.updateDirEntry(op, cfg, d, d.Name, t)
}

func (ei ei) updateDirEntry(op errors.Op, cfg upspin.Config, d *upspin.DirEntry, newName upspin.PathName, newTime upspin.Time) error {
	parsed, err := path.Parse(d.Name)
	if err != nil {
		return errors.E(op, err)
	}
	parsedNew, err := path.Parse(newName)
	if err != nil {
		return errors.E(op, err)
	}
	newName = parsedNew.Path()

	if d.IsDir() && !parsed.Equal(parsedNew) {
		return errors.E(op, d.Name, errors.IsDir, "cannot rename directory")
	}
	if err := pack.CheckPacking(ei, d); err != nil {
		return errors.E(op, errors.Invalid, d.Name, err)
	}

	dkey := make([]byte, aesKeyLen)
	sig, sig2, cipherSum, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return errors.E(op, errors.Invalid, d.Name, err)
	}

	// The writer has a well-known public key.
	writerRawPubKey, err := packutil.GetPublicKey(cfg, d.Writer)
	if err != nil {
		return errors.E(op, d.Name, err)
	}
	writerPubKey, err := factotum.ParsePublicKey(writerRawPubKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Verify that this was signed with the writer's old or new public key.
	f := cfg.Factotum()
	vhash := f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, cipherSum)
	if !ecdsa.Verify(writerPubKey, vhash, sig.R, sig.S) &&
		!ecdsa.Verify(writerPubKey, vhash, sig2.R, sig2.S) {
		// Check sig2 in case writerPubKey is rotating.
		return errors.E(op, d.Name, errVerify)
	}

	// Compute new signature, using the new name.
	d.Writer = cfg.UserName()
	d.SignedName = newName
	d.Time = newTime
	vhash = f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, cipherSum)
	sig, err = f.FileSign(vhash)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Serialize packer metadata. We do not reallocate Packdata since the new data
	// should be the same size or smaller.
	if err := pdMarshal(&d.Packdata, sig, sig0, cipherSum); err != nil {
		return errors.E(op, d.Name, err)
	}
	d.Name = newName

	return nil
}

// Countersign uses the key in factotum f to add a signature to a DirEntry that is already signed by oldKey.
func (ei ei) Countersign(oldKey upspin.PublicKey, f upspin.Factotum, d *upspin.DirEntry) error {
	const op errors.Op = "pack/eeintegrity.Countersign"
	if d.IsDir() {
		return errors.E(op, d.Name, errors.IsDir, "cannot sign directory")
	}

	// Get ECDSA form of old key.
	oldPubKey, err := factotum.ParsePublicKey(oldKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Extract existing signatures, but keep only the newest.
	sig, _, cipherSum, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return errors.E(op, d.Name, errors.Invalid, err)
	}

	// Verify existing signature with oldKey.
	dkey := make([]byte, aesKeyLen)
	vhash := f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, cipherSum)
	if !ecdsa.Verify(oldPubKey, vhash, sig.R, sig.S) {
		return errors.E(op, d.Name, errVerify, "unable to verify existing signature")
	}

	// Sign with newKey.
	sig1, err := f.FileSign(vhash)
	if err != nil {
		return errors.E(op, d.Name, errVerify, "unable to make new signature")
	}
	pdMarshal(&d.Packdata, sig1, sig, cipherSum)
	return nil
}

func (ei ei) UnpackableByAll(d *upspin.DirEntry) (bool, error) {
	// Content is not encrypted, so anyone can read it.
	return true, nil
}

func pdMarshal(dst *[]byte, sig, sig2 upspin.Signature, cipherSum []byte) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation.
	n := packdataLen()
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	n = 0
	n += packutil.PutBytes((*dst)[n:], sig.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig.S.Bytes())
	if sig2.R == nil {
		zero := big.NewInt(0)
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += packutil.PutBytes((*dst)[n:], sig2.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig2.S.Bytes())
	n += packutil.PutBytes((*dst)[n:], cipherSum)
	*dst = (*dst)[:n]
	return nil
}

func pdUnmarshal(pd []byte) (sig, sig2 upspin.Signature, hash []byte, err error) {
	if len(pd) == 0 {
		return sig0, sig0, nil, errors.Str("nil packdata")
	}
	n := 0
	sig.R = big.NewInt(0)
	sig.S = big.NewInt(0)
	sig2.R = big.NewInt(0)
	sig2.S = big.NewInt(0)
	buf := make([]byte, marshalBufLen)
	n += packutil.GetBytes(&buf, pd[n:])
	sig.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig.S.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig2.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig2.S.SetBytes(buf)
	hash = make([]byte, sha256.Size)
	n += packutil.GetBytes(&hash, pd[n:])
	if hash == nil {
		return sig0, sig0, nil, errors.Errorf("pdUnmarshal: file hash is required")
	}
	return sig, sig2, hash, nil
}

// packdataLen returns n big enough for packing, sig.R, sig.S
func packdataLen() int {
	return 2*marshalBufLen + binary.MaxVarintLen64 + sha256.Size + 1
}
