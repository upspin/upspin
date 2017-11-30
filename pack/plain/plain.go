// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package plain is a simple Packing that passes the data untouched.
// The pathname, packing, and time are signed.
package plain // import "upspin.io/pack/plain"

import (
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
	zero                = big.NewInt(0)
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
	const op errors.Op = "pack/plain.Pack"
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
	const op errors.Op = "pack/plain.blockPacker.Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, errors.E(op, err)
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
	const op errors.Op = "pack/plain.blockPacker.Close"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return errors.E(op, err)
	}

	// Compute entry signature with dkey=sum=0.
	f := bp.cfg.Factotum()
	e := bp.entry
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	sig, err := f.FileSign(f.DirEntryHash(e.SignedName, e.Link, e.Attr, e.Packing, e.Time, dkey, sum))
	if err != nil {
		return errors.E(op, err)
	}
	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{})
}

// Unpack implements upspin.Packer.
func (p plainPack) Unpack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op errors.Op = "pack/plain.Unpack"
	if err := pack.CheckPacking(p, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}

	// Call Size to check that the block Offsets and Sizes are consistent.
	if _, err := d.Size(); err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	sig, sig2, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return nil, errors.E(op, d.Name, err)
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
	sum := make([]byte, sha256.Size)
	// Verify that this was signed with the writer's old or new public key.
	vhash := f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, sum)
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
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	cleartext = ciphertext
	return
}

func (bp *blockUnpacker) Close() error {
	return nil
}

// Name implements upspin.Name.
func (p plainPack) Name(cfg upspin.Config, d *upspin.DirEntry, newName upspin.PathName) error {
	const op errors.Op = "pack/plain.Name"
	return p.updateDirEntry(op, cfg, d, newName, d.Time)
}

// SetTime implements upspin.SetTime.
func (p plainPack) SetTime(cfg upspin.Config, d *upspin.DirEntry, t upspin.Time) error {
	const op errors.Op = "pack/plain.SetTime"
	return p.updateDirEntry(op, cfg, d, d.Name, t)
}

func (p plainPack) updateDirEntry(op errors.Op, cfg upspin.Config, dirEntry *upspin.DirEntry, newName upspin.PathName, newTime upspin.Time) error {
	parsed, err := path.Parse(dirEntry.Name)
	if err != nil {
		return errors.E(op, err)
	}
	parsedNew, err := path.Parse(newName)
	if err != nil {
		return errors.E(op, err)
	}
	newName = parsedNew.Path()

	if dirEntry.IsDir() && !parsed.Equal(parsedNew) {
		return errors.E(op, dirEntry.Name, errors.IsDir, "cannot rename directory")
	}
	dirEntry.Name = newName
	dirEntry.SignedName = dirEntry.Name
	dirEntry.Time = newTime

	// Update entry signature.
	f := cfg.Factotum()
	e := dirEntry
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	sig, err := f.FileSign(f.DirEntryHash(e.SignedName, e.Link, e.Attr, e.Packing, e.Time, dkey, sum))
	if err != nil {
		return errors.E(op, err)
	}
	return pdMarshal(&dirEntry.Packdata, sig, upspin.Signature{})
}

// Countersign uses the key in factotum f to add a signature to a DirEntry that is already signed by oldKey.
func (p plainPack) Countersign(oldKey upspin.PublicKey, f upspin.Factotum, d *upspin.DirEntry) error {
	const op errors.Op = "pack/plain.Countersign"
	if d.IsDir() {
		return errors.E(op, d.Name, errors.IsDir, "cannot sign directory")
	}

	// Get ECDSA form of old key.
	oldPubKey, err := factotum.ParsePublicKey(oldKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Extract existing signatures, but keep only the newest.
	sig, _, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return errors.E(op, d.Name, errors.Invalid, err)
	}

	// Verify existing signature with oldKey.
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	vhash := f.DirEntryHash(d.SignedName, d.Link, d.Attr, d.Packing, d.Time, dkey, sum)
	if !ecdsa.Verify(oldPubKey, vhash, sig.R, sig.S) {
		return errors.E(op, d.Name, errVerify, "unable to verify existing signature")
	}

	// Sign with newKey.
	sig1, err := f.FileSign(vhash)
	if err != nil {
		return errors.E(op, d.Name, errVerify, "unable to make new signature")
	}
	pdMarshal(&d.Packdata, sig1, sig)
	return nil
}

func (p plainPack) UnpackableByAll(d *upspin.DirEntry) (bool, error) {
	// Content is not encrypted, so anyone can read it.
	return true, nil
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
	// sig2 is a signature with another owner key, to enable smoother key rotation.
	n := packdataLen()
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	n = 0
	n += packutil.PutBytes((*dst)[n:], sig.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig.S.Bytes())
	if sig2.R == nil {
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += packutil.PutBytes((*dst)[n:], sig2.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig2.S.Bytes())
	*dst = (*dst)[:n]
	return nil
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
	n += packutil.GetBytes(&buf, pd[n:])
	sig.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig.S.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig2.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, pd[n:])
	sig2.S.SetBytes(buf)
	return sig, sig2, nil
}

// packdataLen returns n big enough for packing, sig.R, sig.S
func packdataLen() int {
	return 2*marshalBufLen + binary.MaxVarintLen64 + 1
}
