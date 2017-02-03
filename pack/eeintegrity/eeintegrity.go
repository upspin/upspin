// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ei implements an elliptic-curve end-to-end integrity-checked packer.
package ei

// This is a copy of pack/ee/ee.go, with the encryption removed.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/path"
	"upspin.io/upspin"
)

var _ upspin.Packer = ei{}

type ei struct{}

const (
	aesKeyLen            = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	marshalBufLen        = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

func init() {
	pack.Register(ei{})
}

var (
	errTooShort           = errors.Str("destination slice too short")
	errVerify             = errors.Str("does not verify")
	errWriter             = errors.Str("empty Writer in Metadata")
	errNoWrappedKey       = errors.Str("no wrapped key for me")
	errKeyLength          = errors.Str("wrong key length for AES-256")
	errNoKnownKeysForUser = errors.Str("no known keys for user")
	errSignedNameNotSet   = errors.Str("empty SignedName")
	sig0                  upspin.Signature // for returning nil of correct type
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
	const op = "pack/ei.Pack"
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
	const op = "pack/ei.blockPacker.Pack"
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
	const op = "pack/ei.blockPacker.Pack"

	const Pack = "Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}

	name := bp.entry.SignedName
	cfg := bp.cfg

	// Compute checksum of block hashes.
	sum := internal.BlockSum(bp.entry.Blocks)

	// Compute entry signature.
	dkey := make([]byte, aesKeyLen)
	sig, err := cfg.Factotum().FileSign(path.Clean(name), bp.entry.Time, dkey, sum)
	if err != nil {
		return errors.E(op, err)
	}

	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{}, sum)
}

// Unpack implements upspin.Packer.
func (ei ei) Unpack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op = "pack/ei.Unpack"
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
		return nil, errors.E(op, d.Name, errors.Str("checksum mismatch"))
	}

	// Fetch writer public key.
	writer := d.Writer
	if len(writer) == 0 {
		return nil, errors.E(op, d.Name, errWriter)
	}
	writerRawPubKey, err := publicKey(cfg, writer)
	if err != nil {
		return nil, errors.E(op, writer, err)
	}
	writerPubKey, writerCurveName, err := factotum.ParsePublicKey(writerRawPubKey)
	if err != nil {
		return nil, errors.E(op, writer, err)
	}

	dkey := make([]byte, aesKeyLen)
	// Verify that this was signed with the writer's old or new public key.
	vhash := factotum.VerHash(writerCurveName, path.Clean(d.SignedName), d.Time, dkey, hash)
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
	const op = "pack/ei.blockUpacker.Unpack"
	// Validate checksum.
	b := sha256.Sum256(ciphertext)
	sum := b[:]
	if got, want := sum, bp.entry.Blocks[bp.Block].Packdata; !bytes.Equal(got, want) {
		return nil, errors.E(op, bp.entry.Name, errors.Str("checksum mismatch"))
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

// Name implements upspin.Packer.
func (ei ei) Name(cfg upspin.Config, d *upspin.DirEntry, newName upspin.PathName) error {
	const op = "pack/ei.Name"
	if d.IsDir() {
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

	// File owner is part of the pathname
	parsed, err := path.Parse(d.Name)
	if err != nil {
		return errors.E(op, err)
	}
	owner := parsed.User()
	// The owner has a well-known public key
	ownerRawPubKey, err := publicKey(cfg, owner)
	if err != nil {
		return errors.E(op, d.Name, err)
	}
	ownerPubKey, ownerCurveName, err := factotum.ParsePublicKey(ownerRawPubKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Verify that this was signed with the owner's old or new public key.
	vhash := factotum.VerHash(ownerCurveName, path.Clean(d.SignedName), d.Time, dkey, cipherSum)
	if !ecdsa.Verify(ownerPubKey, vhash, sig.R, sig.S) &&
		!ecdsa.Verify(ownerPubKey, vhash, sig2.R, sig2.S) {
		// Check sig2 in case ownerPubKey is rotating.
		return errors.E(op, d.Name, errVerify)
	}

	parsedNew, err := path.Parse(newName)
	if err != nil {
		return errors.E(op, err)
	}
	newName = parsedNew.Path()

	// Compute new signature, using the new name.
	d.SignedName = newName
	sig, err = cfg.Factotum().FileSign(newName, d.Time, dkey, cipherSum)
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

func pdMarshal(dst *[]byte, sig, sig2 upspin.Signature, cipherSum []byte) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation
	n := packdataLen(0)
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	n = 0
	n += pdPutBytes((*dst)[n:], sig.R.Bytes())
	n += pdPutBytes((*dst)[n:], sig.S.Bytes())
	if sig2.R == nil {
		zero := big.NewInt(0)
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += pdPutBytes((*dst)[n:], sig2.R.Bytes())
	n += pdPutBytes((*dst)[n:], sig2.S.Bytes())
	n += pdPutBytes((*dst)[n:], cipherSum)
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
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
	n += pdGetBytes(&buf, pd[n:])
	sig.R.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig.S.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig2.R.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig2.S.SetBytes(buf)
	hash = make([]byte, sha256.Size)
	n += pdGetBytes(&hash, pd[n:])
	if hash == nil {
		return sig0, sig0, nil, errors.Errorf("pdUnmarshal: file hash is required")
	}
	return sig, sig2, hash, nil
}

// pdPutBytes puts length header in dst and then copies src to dst; returns bytes consumed
func pdPutBytes(dst, src []byte) int {
	vlen := binary.PutVarint(dst, int64(len(src)))
	return vlen + copy(dst[vlen:], src)
}

// pdGetBytes copies (part of) src to dst, based on length header; returns bytes consumed
func pdGetBytes(dst *[]byte, src []byte) int {
	n, vlen := binary.Varint(src)
	*dst = (*dst)[:n]
	k := copy(*dst, src[vlen:n+int64(vlen)])
	if int64(k) != n {
		// can't happen unless dst too short?
		*dst = (*dst)[:0]
		return k + vlen
	}
	return k + vlen
}

// packdataLen returns n big enough for packing, sig.R, sig.S, nwrap, {keyHash, encrypted, nonce, X, y}
func packdataLen(nwrap int) int {
	return 2*marshalBufLen + (1+5*nwrap)*binary.MaxVarintLen64 +
		nwrap*(sha256.Size+(aesKeyLen+gcmTagSize)+gcmStandardNonceSize+2*marshalBufLen) +
		sha256.Size + 1
}

// publicKey returns the string representation of a user's public key.
func publicKey(cfg upspin.Config, user upspin.UserName) (upspin.PublicKey, error) {

	// Key pairs have three representations:
	// 1. string, used for storage and between programs like User.Lookup
	// 2. ecdsa, internal binary format for computation
	// 3. a secret seed sufficient to reconstruct the key pair
	// In form 1, the first bytes describe the packing name, e.g. "p256".
	// In form 2, there is an Curve field in the struct that plays that role.
	// Form 3, used only in keygen.go, is simply 128 bits of entropy.

	// Are we requesting our own public key?
	if string(user) == string(cfg.UserName()) {
		return cfg.Factotum().PublicKey(), nil
	}
	keyServer, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		return "", err
	}
	u, err := keyServer.Lookup(user)
	if err != nil {
		return "", err
	}
	if len(u.PublicKey) == 0 {
		return "", errors.E(user, errors.NotExist, errNoKnownKeysForUser)
	}
	return u.PublicKey, nil
}
