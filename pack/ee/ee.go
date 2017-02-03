// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ee implements an elliptic-curve end-to-end encryption packer.
package ee

// Upspin ee crypto summary:
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext to a storage server
// and metadata to a directory server.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"

	"golang.org/x/crypto/hkdf"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/path"
	"upspin.io/upspin"
)

// wrappedKey encodes a key that will decrypt and verify the ciphertext.
type wrappedKey struct {
	keyHash   []byte // recipient's public key
	dkey      []byte // ciphertext symmetric decryption key
	nonce     []byte
	ephemeral ecdsa.PublicKey
}

type keyHashArray [sha256.Size]byte // sometimes we need the array

// ecdsaKeyHash returns the hash of a key given in ECDSA format
// and is the binary-format counterpart of KeyHash in package factotum.
func ecdsaKeyHash(p *ecdsa.PublicKey) []byte {
	name, ok := ellipticNames[p.Curve.Params().Name]
	if !ok { // supposedly can't construct such an ecdsa.PublicKey
		log.Error.Printf("pack/ee: unrecognized key type: %s", p.Curve.Params().Name)
		return nil
	}
	keyBytes := upspin.PublicKey(fmt.Sprintf("%s\n%s\n%s\n", name, p.X.String(), p.Y.String()))
	// this string should be the same as the file contents ~/.ssh/public.upspinkey
	return factotum.KeyHash(keyBytes)
}

var _ upspin.Packer = ee{}

type ee struct{}

const (
	aesKeyLen            = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	marshalBufLen        = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

func init() {
	pack.Register(ee{})
	ellipticNames = map[string]string{
		elliptic.P256().Params().Name: "p256",
		elliptic.P384().Params().Name: "p384",
		elliptic.P521().Params().Name: "p521",
	}
}

var (
	errTooShort           = errors.Str("destination slice too short")
	errVerify             = errors.Str("does not verify")
	errWriter             = errors.Str("empty Writer in Metadata")
	errNoWrappedKey       = errors.Str("no wrapped key for me")
	errKeyLength          = errors.Str("wrong key length for AES-256")
	errNoKnownKeysForUser = errors.Str("no known keys for user")
	errSignedNameNotSet   = errors.Str("empty SignedName")
	sig0                  upspin.Signature  // for returning error of correct type
	ellipticNames         map[string]string // ellipticNames maps ECDSA curve names to upspin-friendly curve names.
)

var errNotOnCurve = errors.Str("a crypto attack was attempted against you; see safecurves.cr.yp.to/twist.html for details")

func (ee ee) Packing() upspin.Packing {
	return upspin.EEPack
}

func (ee ee) PackLen(cfg upspin.Config, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ee, d); err != nil {
		return -1
	}
	return len(cleartext)
}

func (ee ee) UnpackLen(cfg upspin.Config, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ee, d); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (ee ee) String() string {
	return "ee"
}

func (ee ee) Pack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const op = "pack/ee.Pack"
	if err := pack.CheckPacking(ee, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}
	if len(d.SignedName) == 0 {
		return nil, errors.E(op, errors.Invalid, d.Name, errSignedNameNotSet)
	}

	// TODO(adg): support append; for now assume a new file.
	d.Blocks = nil

	dkey, blockCipher, err := newKeyAndCipher()
	if err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	return &blockPacker{
		cfg:    cfg,
		entry:  d,
		cipher: blockCipher,
		dkey:   dkey,
	}, nil
}

func newKeyAndCipher() ([]byte, cipher.Block, error) {
	// Pick fresh file encryption key.
	dkey := make([]byte, aesKeyLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return nil, nil, err
	}
	// This shouldn't happen, but be paranoid.
	if len(dkey) != aesKeyLen {
		return nil, nil, errKeyLength
	}

	// Set up the block cipher.
	blockCipher, err := aes.NewCipher(dkey)
	if err != nil {
		return nil, nil, err
	}

	return dkey, blockCipher, nil
}

type blockPacker struct {
	cfg    upspin.Config
	entry  *upspin.DirEntry
	cipher cipher.Block
	dkey   []byte

	buf internal.LazyBuffer
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	const op = "pack/ee.blockPacker.Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	// Compute offset of this block,
	// the size of the preceding blocks.
	offs, err := bp.entry.Size()
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}

	// Encrypt.
	ciphertext = bp.buf.Bytes(len(cleartext))
	if err := crypt(ciphertext, cleartext, bp.cipher, offs); err != nil {
		return nil, errors.E(op, err)
	}

	// Compute size and checksum.
	size := int64(len(ciphertext))
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

func (bp *blockPacker) SetLocation(l upspin.Location) {
	bs := bp.entry.Blocks
	bs[len(bs)-1].Location = l
}

func (bp *blockPacker) Close() error {
	const op = "pack/ee.blockPacker.Pack"
	// Zero out encryption key when we're done.
	defer zeroSlice(&bp.dkey)

	const Pack = "Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}

	name := bp.entry.SignedName
	cfg := bp.cfg

	// Wrap keys.
	wrap := make([]wrappedKey, 2)

	// First, wrap for myself.
	p, _, err := factotum.ParsePublicKey(cfg.Factotum().PublicKey())
	if err != nil {
		return errors.E(op, name, err)
	}
	wrap[0], err = aesWrap(p, bp.dkey)
	if err != nil {
		return errors.E(op, name, err)
	}

	// Also wrap for owner, if different.
	parsed, err := path.Parse(name)
	if err != nil {
		return errors.E(op, name, err)
	}
	owner := parsed.User()
	if owner == cfg.UserName() {
		wrap = wrap[:1]
	} else {
		keyServer, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
		if err != nil {
			return errors.E(op, name, err)
		}
		u, err := keyServer.Lookup(owner)
		if err != nil {
			return errors.E(op, name, owner, err)
		}
		ownerKey := u.PublicKey
		if ownerKey == cfg.Factotum().PublicKey() {
			log.Debug.Printf("pack/ee: %q and %q have the same keys", owner, cfg.UserName())
			wrap = wrap[:1]
		} else {
			p, _, err = factotum.ParsePublicKey(ownerKey)
			if err != nil {
				return errors.E(op, name, owner, err)
			}
			wrap[1], err = aesWrap(p, bp.dkey)
			if err != nil {
				return errors.E(op, name, owner, err)
			}
		}
	}

	// Compute checksum of block hashes.
	sum := internal.BlockSum(bp.entry.Blocks)

	// Compute entry signature.
	sig, err := cfg.Factotum().FileSign(path.Clean(name), bp.entry.Time, bp.dkey, sum)
	if err != nil {
		return errors.E(op, err)
	}

	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{}, wrap, sum)
}

func (ee ee) Unpack(cfg upspin.Config, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const op = "pack/ee.Unpack"
	if err := pack.CheckPacking(ee, d); err != nil {
		return nil, errors.E(op, errors.Invalid, d.Name, err)
	}

	// Call Size to check that the block Offsets and Sizes are consistent.
	if _, err := d.Size(); err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	sig, sig2, wrap, hash, err := pdUnmarshal(d.Packdata)
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

	// Fetch my own keys, as I am the recipient of the file.
	me := cfg.UserName()
	rawPublicKey, err := publicKey(cfg, me)
	if err != nil {
		return nil, errors.E(op, d.Name, err)
	}

	// Pull the decryption key out of the wrapped keys.
	dkey := make([]byte, aesKeyLen)
	// For quick lookup, hash my public key and locate my wrapped key in the metadata.
	rhash := factotum.KeyHash(rawPublicKey)
	for _, w := range wrap {
		if !bytes.Equal(rhash, w.keyHash) {
			continue
		}
		// Decode my wrapped key using my private key.
		dkey, err = aesUnwrap(cfg.Factotum(), w)
		if err != nil {
			return nil, errors.E(op, d.Name, me, err)
		}
		if len(dkey) != aesKeyLen {
			return nil, errors.E(op, d.Name, errKeyLength)
		}
		// Verify that this was signed with the writer's old or new public key.
		vhash := factotum.VerHash(writerCurveName, path.Clean(d.SignedName), d.Time, dkey, hash)
		if !ecdsa.Verify(writerPubKey, vhash, sig.R, sig.S) &&
			!ecdsa.Verify(writerPubKey, vhash, sig2.R, sig2.S) {
			// Check sig2 in case writerPubKey is rotating.
			return nil, errors.E(op, d.Name, writer, errVerify)
			// TODO(ehg) If reader is owner, consider trying even older factotum keys.
		}
		blockCipher, err := aes.NewCipher(dkey)
		if err != nil {
			return nil, errors.E(op, err)
		}
		// We're OK to start decrypting blocks.
		return &blockUnpacker{
			cfg:          cfg,
			entry:        d,
			BlockTracker: internal.NewBlockTracker(d.Blocks),
			cipher:       blockCipher,
		}, nil
	}
	return nil, errors.E(op, errors.CannotDecrypt, d.Name, me)
}

type blockUnpacker struct {
	cfg                   upspin.Config
	entry                 *upspin.DirEntry
	internal.BlockTracker // provides NextBlock method and Block field
	cipher                cipher.Block

	buf internal.LazyBuffer
}

func (bp *blockUnpacker) Unpack(ciphertext []byte) (cleartext []byte, err error) {
	const op = "pack/ee.blockUpacker.Unpack"
	// Validate checksum.
	b := sha256.Sum256(ciphertext)
	sum := b[:]
	if got, want := sum, bp.entry.Blocks[bp.Block].Packdata; !bytes.Equal(got, want) {
		return nil, errors.E(op, bp.entry.Name, errors.Str("checksum mismatch"))
	}

	cleartext = bp.buf.Bytes(len(ciphertext))

	// Decrypt.
	if err := crypt(cleartext, ciphertext, bp.cipher, bp.entry.Blocks[bp.Block].Offset); err != nil {
		return nil, errors.E(op, bp.entry.Name, err)
	}

	return cleartext, nil
}

func (bp *blockUnpacker) Close() error {
	return nil
}

// ReaderHashes returns SHA-256 hashes of the public keys able to decrypt the associated ciphertext.
func (ee ee) ReaderHashes(packdata []byte) (readers [][]byte, err error) {
	const op = "pack/ee.ReaderHashes"
	_, _, wrap, _, err := pdUnmarshal(packdata)
	if err != nil {
		return nil, errors.E(op, errors.Invalid, err)
	}
	readers = make([][]byte, len(wrap))
	for i := 0; i < len(wrap); i++ {
		readers[i] = wrap[i].keyHash
	}
	return readers, nil
}

// Share extracts the file decryption key from the packdata, wraps it for a revised list of readers, and updates packdata.
func (ee ee) Share(cfg upspin.Config, readers []upspin.PublicKey, packdata []*[]byte) {

	// A Packdata holds a cipherSum, a Signature, and a list of wrapped keys.
	// Share updates the wrapped keys, leaving the other two fields unchanged.
	// For efficiency, Share() reuses the wrapped key for readers common to the old and new lists.

	// TODO(ehg) Check that wrapping for owner and writer are retained.

	// Fetch all the public keys we'll need.
	pubkey := make([]*ecdsa.PublicKey, len(readers))
	hash := make([]keyHashArray, len(readers))
	for i, pub := range readers {
		var err error
		pubkey[i], _, err = factotum.ParsePublicKey(pub)
		if err != nil {
			continue
		}
		copy(hash[i][:], factotum.KeyHash(pub))
	}

	// For each packdata, wrap for new readers.
	for j, d := range packdata {

		// Extract dkey and existing wrapped keys from packdata.
		var dkey []byte
		alreadyWrapped := make(map[keyHashArray]*wrappedKey)
		sig, sig2, wrap, cipherSum, err := pdUnmarshal(*d)
		if err != nil {
			log.Error.Printf("pack/ee.Share: pdUnmarshal failed: %v", err)
			for jj := j; j < len(packdata); jj++ {
				packdata[jj] = nil
			}
			return
		}
		for i, w := range wrap {
			var h keyHashArray
			copy(h[:], w.keyHash)
			alreadyWrapped[h] = &wrap[i]
			_, err := cfg.Factotum().PublicKeyFromHash(w.keyHash)
			if err != nil {
				// to unwrap dkey, we can only use our own private keys
				continue
			}
			dkey, err = aesUnwrap(cfg.Factotum(), w)
			if err != nil {
				log.Error.Printf("pack/ee: dkey unwrap failed: %v", err)
				break
			}
		}
		if len(dkey) == 0 { // Failed to get a valid decryption key.
			packdata[j] = nil // Tell caller this packdata was skipped.
			continue
		}

		// Create new list of wrapped keys.
		wrap = make([]wrappedKey, len(readers))
		nwrap := 0
		for i := range readers {
			if pubkey[i] == nil {
				continue
			}
			pw, ok := alreadyWrapped[hash[i]]
			if !ok { // then need to wrap
				w, err := aesWrap(pubkey[i], dkey)
				if err != nil {
					continue
				}
				pw = &w
			} // else reuse the existing wrapped dkey.
			wrap[nwrap] = *pw
			nwrap++
		}
		wrap = wrap[:nwrap]

		// Rebuild packdata[j] from existing sig and new wrapped keys.
		dst := make([]byte, packdataLen(nwrap))
		if pdMarshal(&dst, sig, sig2, wrap, cipherSum) != nil {
			packdata[j] = nil // Tell caller this packdata was skipped.
		} else {
			*packdata[j] = dst
		}
	}
}

// Name implements upspin.Name.
func (ee ee) Name(cfg upspin.Config, d *upspin.DirEntry, newName upspin.PathName) error {
	const op = "pack/ee.Name"
	if d.IsDir() {
		return errors.E(op, d.Name, errors.IsDir, "cannot rename directory")
	}
	if err := pack.CheckPacking(ee, d); err != nil {
		return errors.E(op, errors.Invalid, d.Name, err)
	}

	dkey := make([]byte, aesKeyLen)
	sig, sig2, wrap, cipherSum, err := pdUnmarshal(d.Packdata)
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

	// Now get my own keys
	me := cfg.UserName() // Recipient of the file is me (the user in the config)
	rawPublicKey, err := publicKey(cfg, me)
	if err != nil {
		return errors.E(op, d.Name, err)
	}
	pubkey, _, err := factotum.ParsePublicKey(rawPublicKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// For quick lookup, hash my public key and locate my wrapped key in the metadata.
	rhash := ecdsaKeyHash(pubkey)
	wrapFound := false
	var w wrappedKey
	for _, w = range wrap {
		if bytes.Equal(rhash, w.keyHash) {
			wrapFound = true
			break
		}
	}
	if !wrapFound {
		return errors.E(op, d.Name, errNoWrappedKey)
	}

	// Decode my wrapped key using my private key
	dkey, err = aesUnwrap(cfg.Factotum(), w)
	if err != nil {
		return errors.E(op, d.Name, errors.Str("unwrap failed"))
	}

	// Verify that this was signed with the owner's old or new public key.
	vhash := factotum.VerHash(ownerCurveName, path.Clean(d.SignedName), d.Time, dkey, cipherSum)
	if !ecdsa.Verify(ownerPubKey, vhash, sig.R, sig.S) &&
		!ecdsa.Verify(ownerPubKey, vhash, sig2.R, sig2.S) {
		// Check sig2 in case ownerPubKey is rotating.
		return errors.E(op, d.Name, errVerify)
	}

	// If we are changing directories, remove all wrapped keys except my own.
	parsedNew, err := path.Parse(newName)
	if err != nil {
		return errors.E(op, err)
	}
	newName = parsedNew.Path()
	if !parsed.Drop(1).Equal(parsedNew.Drop(1)) {
		wrap = []wrappedKey{w}
	}

	// Compute new signature, using the new name.
	d.SignedName = newName
	sig, err = cfg.Factotum().FileSign(newName, d.Time, dkey, cipherSum)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Serialize packer metadata. We do not reallocate Packdata since the new data
	// should be the same size or smaller.
	if err := pdMarshal(&d.Packdata, sig, sig0, wrap, cipherSum); err != nil {
		return errors.E(op, d.Name, err)
	}
	d.Name = newName

	return nil
}

// Countersign uses the key in factotum f to add a signature to a DirEntry that is already signed by oldKey.
func Countersign(oldKey upspin.PublicKey, f upspin.Factotum, d *upspin.DirEntry) error {
	// TODO(ehg) Consolidate shared code amongst Countersign, Name, Share.
	const op = "pack/ee.Countersign"
	if d.IsDir() || d.IsLink() {
		return errors.E(op, d.Name, errors.IsDir, "cannot sign directory or link")
	}

	// Get ECDSA forms of keys.
	oldPubKey, oldCurveName, err := factotum.ParsePublicKey(oldKey)
	if err != nil {
		return errors.E(op, d.Name, err)
	}

	// Extract existing signature
	sig, _, wrap, cipherSum, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return errors.E(op, d.Name, errors.Invalid, err)
	}

	// Get wrapped key.
	rhash := ecdsaKeyHash(oldPubKey)
	wrapFound := false
	var w wrappedKey
	for _, w = range wrap {
		if bytes.Equal(rhash, w.keyHash) {
			wrapFound = true
			break
		}
	}
	if !wrapFound {
		return errors.E(op, d.Name, errNoWrappedKey)
	}
	dkey := make([]byte, aesKeyLen)
	dkey, err = aesUnwrap(f, w)
	if err != nil {
		return errors.E(op, d.Name, errors.Str("unwrap failed"))
	}

	// Verify existing signature with oldKey.
	name := path.Clean(d.SignedName)
	vhash := factotum.VerHash(oldCurveName, name, d.Time, dkey, cipherSum)
	if !ecdsa.Verify(oldPubKey, vhash, sig.R, sig.S) {
		return errors.E(op, d.Name, errVerify, "unable to verify existing signature")
	}

	// Sign with newKey.
	sig0, err := f.FileSign(name, d.Time, dkey, cipherSum)
	if err != nil {
		return errors.E(op, d.Name, errVerify, "unable to make new signature")
	}
	pdMarshal(&d.Packdata, sig0, sig, wrap, cipherSum)
	return nil
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func aesWrap(R *ecdsa.PublicKey, dkey []byte) (w wrappedKey, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral key pair
	// S = vR   shared point
	curve := R.Curve
	// TODO(ehg)  Confirm that curve is one of our approved curves.
	if !curve.IsOnCurve(R.X, R.Y) {
		err = errNotOnCurve
		return
	}
	v, err := ecdsa.GenerateKey(curve, rand.Reader)
	sx, sy := curve.ScalarMult(R.X, R.Y, v.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)
	w.ephemeral = ecdsa.PublicKey{Curve: curve, X: v.X, Y: v.Y}

	// Step 2.  Convert shared secret to strong secret via HKDF.
	w.nonce = make([]byte, gcmStandardNonceSize)
	_, err = rand.Read(w.nonce)
	if err != nil {
		return
	}
	w.keyHash = ecdsaKeyHash(R)
	mess := []byte(fmt.Sprintf("%02x:%x:%x", upspin.EEPack, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO(security-reviewer) reconsider salt
	strong := make([]byte, aesKeyLen)
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		return
	}

	// Step 3. Encrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	w.dkey = make([]byte, 0, len(dkey)+gcmTagSize)
	w.dkey = aead.Seal(w.dkey, w.nonce, dkey, nil)
	// TODO(ehg) figure out why aead.Seal allocated memory here
	return
}

// Extract per-file symmetric key from w.
// If error, len(dkey)==0.
func aesUnwrap(f upspin.Factotum, w wrappedKey) (dkey []byte, err error) {
	myPub, err := f.PublicKeyFromHash(w.keyHash)
	if err != nil {
		return nil, err
	}
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	pub, _, err := factotum.ParsePublicKey(myPub)
	if err != nil {
		return nil, err
	}
	sx, sy, err := f.ScalarMult(w.keyHash, pub.Curve, w.ephemeral.X, w.ephemeral.Y)
	if err != nil {
		return nil, err
	}
	S := elliptic.Marshal(pub.Curve, sx, sy)

	// Step 2.  Convert shared secret to strong secret via HKDF.
	mess := []byte(fmt.Sprintf("%02x:%x:%x", upspin.EEPack, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess)
	strong := make([]byte, aesKeyLen)
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		return
	}

	// Step 3. Decrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	dkey = make([]byte, 0, aesKeyLen)
	dkey, err = aead.Open(dkey, w.nonce, w.dkey, nil)
	if err != nil {
		dkey = dkey[:0]
	}
	return
}

func pdMarshal(dst *[]byte, sig, sig2 upspin.Signature, wrap []wrappedKey, cipherSum []byte) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation
	n := packdataLen(len(wrap))
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
	n += binary.PutVarint((*dst)[n:], int64(len(wrap)))
	for _, w := range wrap {
		n += pdPutBytes((*dst)[n:], w.keyHash)
		n += pdPutBytes((*dst)[n:], w.dkey)
		n += pdPutBytes((*dst)[n:], w.nonce)
		n += pdPutBytes((*dst)[n:], w.ephemeral.X.Bytes())
		n += pdPutBytes((*dst)[n:], w.ephemeral.Y.Bytes())
	}
	n += pdPutBytes((*dst)[n:], cipherSum)
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

func pdUnmarshal(pd []byte) (sig, sig2 upspin.Signature, wrap []wrappedKey, hash []byte, err error) {
	if len(pd) == 0 {
		return sig0, sig0, nil, nil, errors.Str("nil packdata")
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
	nwrap64, vlen := binary.Varint(pd[n:])
	n += vlen
	nwrap := int(nwrap64)
	if int64(nwrap) != nwrap64 {
		return sig0, sig0, nil, nil, errors.Errorf("implausible number of wrapped keys: %d\n", nwrap64)
	}
	wrap = make([]wrappedKey, nwrap)
	for i := 0; i < nwrap; i++ {
		var w wrappedKey
		w.keyHash = make([]byte, sha256.Size)
		w.dkey = make([]byte, aesKeyLen+gcmTagSize)
		w.nonce = make([]byte, gcmStandardNonceSize)
		w.ephemeral = ecdsa.PublicKey{X: big.NewInt(0), Y: big.NewInt(0)}
		n += pdGetBytes(&w.keyHash, pd[n:])
		n += pdGetBytes(&w.dkey, pd[n:])
		n += pdGetBytes(&w.nonce, pd[n:])
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.X.SetBytes(buf)
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.Y.SetBytes(buf)
		if w.ephemeral.Y.BitLen() > 393 {
			w.ephemeral.Curve = elliptic.P521()
		} else if w.ephemeral.Y.BitLen() > 265 {
			w.ephemeral.Curve = elliptic.P384()
		} else {
			w.ephemeral.Curve = elliptic.P256()
		}
		wrap[i] = w
	}
	hash = make([]byte, sha256.Size)
	n += pdGetBytes(&hash, pd[n:])
	if hash == nil {
		return sig0, sig0, nil, nil, errors.Errorf("pdUnmarshal: file hash is required")
	}
	return sig, sig2, wrap, hash, nil
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

// zeroSlice replaces the contents of the given slice with zeroes.
func zeroSlice(b *[]byte) {
	for i := range *b {
		(*b)[i] = 0
	}
}

// crypt [enc|de]crypts the input bytes into the output slice
// with the provided key for the given DirBlock.
func crypt(out, in []byte, blockCipher cipher.Block, offset int64) error {
	const streamBufferSize = 512  // as defined in $GOROOT/src/crypto/cipher/ctr.go
	bs := blockCipher.BlockSize() // 16 bytes in practice

	// We start with a zero iv because we're certain that the
	// encryption key is random and not reused anywhere.
	iv := make([]byte, bs)

	// Set the initialization vector to whatever it was at the start of the
	// nearest (looking backward) stream buffer.
	ivStart := (offset - (offset % streamBufferSize)) / int64(bs)
	iv[bs-1] = byte(ivStart)
	iv[bs-2] = byte(ivStart >> 8)
	iv[bs-3] = byte(ivStart >> 16)
	iv[bs-4] = byte(ivStart >> 24)

	ctr := cipher.NewCTR(blockCipher, iv)

	// If this offset is not an even multiple of streamBufferSize
	// xor some empty data to synchronize it.
	if n := int(offset % streamBufferSize); n > 0 {
		ignore := make([]byte, n)
		ctr.XORKeyStream(ignore, ignore)
	}

	// Encrypt the block.
	ctr.XORKeyStream(out, in)

	return nil
}
