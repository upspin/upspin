// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ee implements elliptic-curve end-to-end-encrypted packers.
package ee

// Upspin ee crypto summary:
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext and metadata to a
// directory server.

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
		log.Printf("unrecognized key type: %s", p.Curve.Params().Name)
		return nil
	}
	keyBytes := upspin.PublicKey(fmt.Sprintf("%s\n%s\n%s\n", name, p.X.String(), p.Y.String()))
	// this string should be the same as the file contents ~/.ssh/public.upspinkey
	return factotum.KeyHash(keyBytes)
}

var _ upspin.Packer = ee{}

type ee struct{}

const (
	aesKeyLen     = 32 // AES-256,random-key because public cloud should withstand multifile multikey attack.
	marshalBufLen = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
)

func init() {
	pack.Register(ee{})
	ellipticNames = map[string]string{
		elliptic.P256().Params().Name: "p256",
		elliptic.P384().Params().Name: "p384",
		elliptic.P521().Params().Name: "p521",
	}
}

const (
	// unfortunately cipher/gcm.go doesn't export these
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

var (
	errTooShort           = errors.Str("destination slice too short")
	errVerify             = errors.Str("does not verify")
	errWriter             = errors.Str("empty Writer in Metadata")
	errNoWrappedKey       = errors.Str("no wrapped key for me")
	errKeyLength          = errors.Str("wrong key length for AES-256")
	errNoKnownKeysForUser = errors.Str("no known keys for user")
	sig0                  upspin.Signature  // for returning nil of correct type
	ellipticNames         map[string]string // ellipticNames maps ECDSA curve names to upspin-friendly curve names.
)

func (ee ee) Packing() upspin.Packing {
	return upspin.EEPack
}

func (ee ee) PackLen(ctx upspin.Context, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ee, d); err != nil {
		return -1
	}
	return len(cleartext)
}

func (ee ee) UnpackLen(ctx upspin.Context, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPacking(ee, d); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (ee ee) String() string {
	return "ee"
}

func (ee ee) Pack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockPacker, error) {
	const Pack = "Pack"
	if err := pack.CheckPacking(ee, d); err != nil {
		return nil, errors.E(Pack, errors.Invalid, d.Name, err)
	}

	// TODO(adg): support append; for now assume a new file.
	d.Blocks = nil

	// Pick fresh file encryption key.
	dkey := make([]byte, aesKeyLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return nil, errors.E(Pack, d.Name, err)
	}

	// Set up the stream cipher.
	if len(dkey) != aesKeyLen {
		return nil, errors.E(Pack, errKeyLength)
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return nil, errors.E(Pack, err)
	}
	iv := make([]byte, aes.BlockSize)
	// iv=0 is ok because we're certain that dkey is random and not reused
	stream := cipher.NewCTR(block, iv)

	return &blockPacker{
		ctx:    ctx,
		entry:  d,
		dkey:   dkey,
		cipher: stream,
	}, nil
}

type blockPacker struct {
	ctx    upspin.Context
	entry  *upspin.DirEntry
	dkey   []byte
	cipher cipher.Stream

	buf internal.LazyBuffer
}

func (bp *blockPacker) Pack(cleartext []byte) (ciphertext []byte, err error) {
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return nil, err
	}

	ciphertext = bp.buf.Bytes(len(cleartext))

	// Encrypt.
	bp.cipher.XORKeyStream(ciphertext, cleartext)

	// Compute size, offset, and checksum.
	size := int64(len(ciphertext))
	offs := bp.entry.Size()
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
	// Zero out encryption key when we're done.
	defer zeroSlice(&bp.dkey)

	const Pack = "Pack"
	if err := internal.CheckLocationSet(bp.entry); err != nil {
		return err
	}

	name := bp.entry.Name
	ctx := bp.ctx

	// Wrap keys.
	wrap := make([]wrappedKey, 2)

	// First, wrap for myself.
	p, _, err := factotum.ParsePublicKey(ctx.Factotum().PublicKey())
	if err != nil {
		return errors.E(Pack, name, err)
	}
	wrap[0], err = aesWrap(p, bp.dkey)
	if err != nil {
		return errors.E(Pack, name, err)
	}

	// Also wrap for owner, if different.
	parsed, err := path.Parse(name)
	if err != nil {
		return errors.E(Pack, name, err)
	}
	owner := parsed.User()
	if owner == ctx.UserName() {
		wrap = wrap[:1]
	} else {
		userConn, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
		if err != nil {
			return errors.E(Pack, name, owner, err)
		}
		u, err := userConn.Lookup(owner)
		if err != nil {
			return errors.E(Pack, name, owner, err)
		}
		ownerKey := u.PublicKey
		if ownerKey == ctx.Factotum().PublicKey() {
			log.Debug.Printf("Is it surprising that %s != %s but they have the same keys?", owner, ctx.UserName())
			wrap = wrap[:1]
		} else {
			p, _, err = factotum.ParsePublicKey(ownerKey)
			if err != nil {
				return errors.E(Pack, name, owner, err)
			}
			wrap[1], err = aesWrap(p, bp.dkey)
			if err != nil {
				return errors.E(Pack, name, owner, err)
			}
		}
	}

	// Compute checksum of block hashes.
	sum := internal.BlockSum(bp.entry.Blocks)

	// Compute entry signature.
	sig, err := ctx.Factotum().FileSign(path.Clean(name), bp.entry.Time, bp.dkey, sum)
	if err != nil {
		return errors.E("Pack", err)
	}

	return pdMarshal(&bp.entry.Packdata, sig, upspin.Signature{}, wrap, sum)
}

func (ee ee) Unpack(ctx upspin.Context, d *upspin.DirEntry) (upspin.BlockUnpacker, error) {
	const Unpack = "Unpack"
	if err := pack.CheckPacking(ee, d); err != nil {
		return nil, errors.E(Unpack, errors.Invalid, d.Name, err)
	}

	sig, sig2, wrap, hash, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return nil, errors.E(Unpack, d.Name, err)
	}

	// Check that our stored+signed block checksum matches the sum of the actual blocks.
	if got, want := internal.BlockSum(d.Blocks), hash; !bytes.Equal(got, want) {
		return nil, errors.E(Unpack, d.Name, errors.Str("checksum mismatch"))
	}

	// Fetch writer public key.
	writer := d.Writer
	if len(writer) == 0 {
		return nil, errors.E(Unpack, d.Name, errWriter)
	}
	writerRawPubKey, err := publicKey(ctx, writer)
	if err != nil {
		return nil, errors.E(Unpack, writer, err)
	}
	writerPubKey, writerCurveName, err := factotum.ParsePublicKey(writerRawPubKey)
	if err != nil {
		return nil, errors.E(Unpack, writer, err)
	}

	// Fetch my own keys, as I am the recipient of the file.
	me := ctx.UserName()
	rawPublicKey, err := publicKey(ctx, me)
	if err != nil {
		return nil, errors.E(Unpack, d.Name, err)
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
		dkey, err = ee.aesUnwrap(ctx.Factotum(), w)
		if err != nil {
			return nil, errors.E(Unpack, d.Name, me, err)
		}
		if len(dkey) != aesKeyLen {
			return nil, errors.E(Unpack, d.Name, errKeyLength)
		}
		// Verify that this was signed with the writer's old or new public key.
		vhash := factotum.VerHash(writerCurveName, path.Clean(d.Name), d.Time, dkey, hash)
		if !ecdsa.Verify(writerPubKey, vhash, sig.R, sig.S) &&
			sig2.R.Sign() != 0 && !ecdsa.Verify(writerPubKey, vhash, sig2.R, sig2.S) {
			// Only check sig2 if non-zero and sig failed, likely because writerPubKey is rotating.
			return nil, errors.E(Unpack, d.Name, writer, errVerify)
		}
		// Set up stream cipher.
		block, err := aes.NewCipher(dkey)
		if err != nil {
			return nil, errors.E(Unpack, d.Name, err)
		}
		iv := make([]byte, aes.BlockSize)
		stream := cipher.NewCTR(block, iv)
		// We're OK to start decrypting blocks.
		return &blockUnpacker{
			ctx:    ctx,
			entry:  d,
			cipher: stream,
			block:  -1,
		}, nil
	}
	return nil, errors.E(Unpack, d.Name, me, errors.Str("could not find wrapped key"))
}

type blockUnpacker struct {
	ctx    upspin.Context
	entry  *upspin.DirEntry
	block  int // index into entry.Blocks
	cipher cipher.Stream

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
	// Validate checksum.
	b := sha256.Sum256(ciphertext)
	sum := b[:]
	if got, want := sum, bp.entry.Blocks[bp.block].Packdata; !bytes.Equal(got, want) {
		return nil, errors.E("Unpack", bp.entry.Name, errors.Str("checksum mismatch"))
	}

	cleartext = bp.buf.Bytes(len(ciphertext))

	// Decrypt.
	bp.cipher.XORKeyStream(cleartext, ciphertext)

	return cleartext, nil
}

// ReaderHashes returns SHA-256 hashes of the public keys able to decrypt the associated ciphertext.
func (ee ee) ReaderHashes(packdata []byte) (readers [][]byte, err error) {
	_, _, wrap, _, err := pdUnmarshal(packdata)
	if err != nil {
		return nil, errors.E("ReaderHashes", errors.Invalid, err)
	}
	readers = make([][]byte, len(wrap))
	for i := 0; i < len(wrap); i++ {
		readers[i] = wrap[i].keyHash
	}
	return readers, nil
}

// Share extracts the file decryption key from the packdata, wraps it for a revised list of readers, and updates packdata.
func (ee ee) Share(ctx upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {

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
	myhash := factotum.KeyHash(ctx.Factotum().PublicKey())

	// For each packdata, wrap for new readers.
	for j, d := range packdata {

		// Extract dkey and existing wrapped keys from packdata.
		var dkey []byte
		alreadyWrapped := make(map[keyHashArray]*wrappedKey)
		sig, sig2, wrap, cipherSum, err := pdUnmarshal(*d)
		if err != nil {
			log.Printf("eePack: pdUnmarshal failed in Share: %v", err)
			for jj := j; j < len(packdata); jj++ {
				packdata[jj] = nil
			}
			return
		}
		for i, w := range wrap {
			var h keyHashArray
			copy(h[:], w.keyHash)
			alreadyWrapped[h] = &wrap[i]
			if !bytes.Equal(myhash, w.keyHash) {
				// to unwrap dkey, we can only use our own private key
				continue
			}
			dkey, err = ee.aesUnwrap(ctx.Factotum(), w)
			if err != nil {
				log.Printf("dkey unwrap failed: %v", err)
				break // give up;  might mean that owner has changed keys
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
				v := w.ephemeral
				log.Printf("Wrap for %x [%d %d]", hash[i], v.X, v.Y)
				// TODO(ehg) Save to a separate log and provide post-analysis.
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
func (ee ee) Name(ctx upspin.Context, d *upspin.DirEntry, newName upspin.PathName) error {
	const Name = "Name"
	if d.IsDir() {
		return errors.E(Name, d.Name, errors.IsDir, "cannot rename directory")
	}
	if err := pack.CheckPacking(ee, d); err != nil {
		return errors.E(Name, errors.Invalid, d.Name, err)
	}

	dkey := make([]byte, aesKeyLen)
	sig, sig2, wrap, cipherSum, err := pdUnmarshal(d.Packdata)
	if err != nil {
		return errors.E(Name, errors.Invalid, d.Name, err)
	}

	// File owner is part of the pathname
	parsed, err := path.Parse(d.Name)
	if err != nil {
		return errors.E(Name, err)
	}
	owner := parsed.User()
	// The owner has a well-known public key
	ownerRawPubKey, err := publicKey(ctx, owner)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}
	ownerPubKey, ownerCurveName, err := factotum.ParsePublicKey(ownerRawPubKey)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}

	// Now get my own keys
	me := ctx.UserName() // Recipient of the file is me (the user in the context)
	rawPublicKey, err := publicKey(ctx, me)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}
	pubkey, _, err := factotum.ParsePublicKey(rawPublicKey)
	if err != nil {
		return errors.E(Name, d.Name, err)
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
		log.Printf("unwrap failed: %s", errNoWrappedKey)
		return errors.E(Name, d.Name, errNoWrappedKey)
	}

	// Decode my wrapped key using my private key
	dkey, err = ee.aesUnwrap(ctx.Factotum(), w)
	if err != nil {
		log.Printf("unwrap failed: %s", err)
		return errors.E(Name, d.Name, errors.Str("unwrap failed"))
	}

	// Verify that this was signed with the owner's old or new public key.
	vhash := factotum.VerHash(ownerCurveName, path.Clean(d.Name), d.Time, dkey, cipherSum)
	if !ecdsa.Verify(ownerPubKey, vhash, sig.R, sig.S) &&
		sig2.R.Sign() != 0 && !ecdsa.Verify(ownerPubKey, vhash, sig2.R, sig2.S) {
		// Only check sig2 if non-zero and sig failed, likely because ownerPubKey is rotating.
		log.Println("verify failed")
		return errors.E(Name, d.Name, errVerify)
	}

	// If we are changing directories, remove all wrapped keys except my own.
	parsedNew, err := path.Parse(newName)
	if err != nil {
		return errors.E(Name, err)
	}
	newName = parsedNew.Path()
	if !parsed.Drop(1).Equal(parsedNew.Drop(1)) {
		wrap = []wrappedKey{w}
	}

	// Compute new signature.
	sig, err = ctx.Factotum().FileSign(newName, d.Time, dkey, cipherSum)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}

	// Serialize packer metadata. We do not reallocate Packdata since the new data
	// should be the same size or smaller.
	if err := pdMarshal(&d.Packdata, sig, sig0, wrap, cipherSum); err != nil {
		return errors.E(Name, d.Name, err)
	}
	d.Name = newName

	return nil
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func aesWrap(R *ecdsa.PublicKey, dkey []byte) (w wrappedKey, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral key pair
	// S = vR   shared point
	curve := R.Curve
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
func (ee ee) aesUnwrap(f upspin.Factotum, w wrappedKey) (dkey []byte, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	pub, _, err := factotum.ParsePublicKey(f.PublicKey())
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
		log.Printf("Error reading from hkdf: %v", err)
		return
	}

	// Step 3. Decrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		log.Printf("Error in creating new cipher block: %v", err)
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("Error in creating new GCM block: %v", err)
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
	// TODO(adg): drop this from the packdata, it's now in DirEntry.Packing.
	(*dst)[0] = byte(upspin.EEPack)
	n = 1
	n += pdPutBytes((*dst)[n:], sig.R.Bytes())
	n += pdPutBytes((*dst)[n:], sig.S.Bytes())
	zero := big.NewInt(0)
	n += pdPutBytes((*dst)[n:], zero.Bytes())
	n += pdPutBytes((*dst)[n:], zero.Bytes())
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
	if pd[0] != byte(upspin.EEPack) {
		return sig0, sig0, nil, nil, errors.Errorf("expected packing %d, got %d", upspin.EEPack, pd[0])
	}
	n := 1
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
	return 1 + 2*marshalBufLen + (1+5*nwrap)*binary.MaxVarintLen64 +
		nwrap*(sha256.Size+(aesKeyLen+gcmTagSize)+gcmStandardNonceSize+2*marshalBufLen) +
		sha256.Size + 1
}

// publicKey returns the string representation of a user's public key.
func publicKey(ctx upspin.Context, user upspin.UserName) (upspin.PublicKey, error) {

	// Key pairs have three representations:
	// 1. string, used for storage and between programs like User.Lookup
	// 2. ecdsa, internal binary format for computation
	// 3. a secret seed sufficient to reconstruct the key pair
	// In form 1, the first bytes describe the packing name, e.g. "p256".
	// In form 2, there is an Curve field in the struct that plays that role.
	// Form 3, used only in keygen.go, is simply 128 bits of entropy.

	log.Debug.Printf("Getting pub key for user: %s", user) // TODO(ehg) Log no longer needed?
	// Are we requesting our own public key?
	if string(user) == string(ctx.UserName()) {
		return ctx.Factotum().PublicKey(), nil
	}
	userService, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		return "", err
	}
	u, err := userService.Lookup(user)
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
