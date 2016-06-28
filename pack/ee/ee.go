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
	errNoWrappedKey       = errors.Str("no wrapped key for me")
	errKeyLength          = errors.Str("wrong key length for AES-256")
	errNoKnownKeysForUser = errors.Str("no known keys for user")
	sig0                  upspin.Signature  // for returning nil of correct type
	ellipticNames         map[string]string // ellipticNames maps ECDSA curve names to upspin-friendly curve names.
)

func (ee ee) Packing() upspin.Packing {
	return upspin.EEPack
}

func (ee ee) PackLen(ctx *upspin.Context, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPackMeta(ee, &d.Metadata); err != nil {
		return -1
	}
	return len(cleartext)
}

func (ee ee) UnpackLen(ctx *upspin.Context, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckUnpackMeta(ee, &d.Metadata); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (ee ee) String() string {
	return "ee"
}

func (ee ee) Pack(ctx *upspin.Context, ciphertext, cleartext []byte, d *upspin.DirEntry) (int, error) {
	const Pack = "Pack"
	if err := pack.CheckPackMeta(ee, &d.Metadata); err != nil {
		return 0, errors.E(Pack, errors.Invalid, d.Name, err)
	}
	if len(ciphertext) < len(cleartext) {
		return 0, errors.E(Pack, errors.Invalid, d.Name, errTooShort)
	}
	ciphertext = ciphertext[:len(cleartext)]

	// Pick fresh file encryption key.
	dkey := make([]byte, aesKeyLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}
	cipherLen, err := ee.encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}
	b := sha256.Sum256(ciphertext)
	cipherSum := b[:]

	// Sign ciphertext.
	sig, err := ctx.Factotum.FileSign(ctx.Packing, path.Clean(d.Name), d.Metadata.Time, dkey, cipherSum)
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}

	// Wrap for myself.
	wrap := make([]wrappedKey, 1)
	p, _, err := factotum.ParsePublicKey(ctx.Factotum.PublicKey())
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}
	wrap[0], err = ee.aesWrap(p, dkey)
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}

	// Serialize packer metadata.
	err = ee.pdMarshal(&d.Metadata.Packdata, sig, sig0, wrap, cipherSum)
	if err != nil {
		return 0, errors.E(Pack, d.Name, err)
	}
	return cipherLen, err
}

func (ee ee) Unpack(ctx *upspin.Context, cleartext, ciphertext []byte, d *upspin.DirEntry) (int, error) {
	const Unpack = "Unpack"
	if err := pack.CheckUnpackMeta(ee, &d.Metadata); err != nil {
		return 0, errors.E(Unpack, errors.Invalid, d.Name, err)
	}
	if len(cleartext) < len(ciphertext) {
		return 0, errors.E(Unpack, errors.Invalid, d.Name, errTooShort)
	}
	cleartext = cleartext[:len(ciphertext)]

	// Retrieve file decryption key.
	dkey := make([]byte, aesKeyLen)
	sig, sig2, wrap, _, err := ee.pdUnmarshal(d.Metadata.Packdata)
	if err != nil {
		return 0, errors.E(Unpack, d.Name, err)
	}

	// File owner is part of the pathname
	parsed, err := path.Parse(d.Name)
	if err != nil {
		return 0, errors.E(Unpack, err)
	}
	owner := parsed.User()
	// The owner has a well-known public key
	ownerRawPubKey, err := publicKey(ctx, owner)
	if err != nil {
		return 0, errors.E(Unpack, d.Name, err)
	}
	ownerPubKey, _, err := factotum.ParsePublicKey(ownerRawPubKey)
	if err != nil {
		return 0, errors.E(Unpack, d.Name, err)
	}

	// Now get my own keys
	me := ctx.UserName // Recipient of the file is me (the user in the context)
	rawPublicKey, err := publicKey(ctx, me)
	if err != nil {
		return 0, errors.E(Unpack, d.Name, err)
	}

	// For quick lookup, hash my public key and locate my wrapped key in the metadata.
	rhash := factotum.KeyHash(rawPublicKey)
	b := sha256.Sum256(ciphertext)
	cipherSum := b[:]
	for _, w := range wrap {
		if !bytes.Equal(rhash, w.keyHash) {
			continue
		}
		// Decode my wrapped key using my private key
		dkey, err = ee.aesUnwrap(ctx.Factotum, w)
		if err != nil {
			log.Printf("unwrap failed: %v", err)
			return 0, errors.E(Unpack, d.Name, err)
		}
		// Verify that this was signed with the owner's old or new public key.
		vhash := factotum.VerHash(ctx.Packing, path.Clean(d.Name), d.Metadata.Time, dkey, cipherSum)
		if !ecdsa.Verify(ownerPubKey, vhash, sig.R, sig.S) &&
			(sig2.R.Sign() != 0 && !ecdsa.Verify(ownerPubKey, vhash, sig2.R, sig2.S)) {
			// Only check sig2 if non-zero and sig failed, likely because ownerPubKey is rotating.
			log.Println("verify failed")
			return 0, errors.E(Unpack, d.Name, errVerify)
		}
		// dkey is safe, so we decrypt the whole blob.
		return ee.decrypt(cleartext, ciphertext, dkey)
	}
	return 0, errors.E(Unpack, d.Name, errNoWrappedKey)
}

// ReaderHashes returns SHA-256 hashes of the public keys able to decrypt the associated ciphertext.
func (ee ee) ReaderHashes(packdata []byte) (readers [][]byte, err error) {
	_, _, wrap, _, err := ee.pdUnmarshal(packdata)
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
func (ee ee) Share(ctx *upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {

	// A Packdata holds a cipherSum, a Signature, and a list of wrapped keys.
	// Share updates the wrapped keys, leaving the other two fields unchanged.
	// For efficiency, Share() reuses the wrapped key for readers common to the old and new lists.

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
	myhash := factotum.KeyHash(ctx.Factotum.PublicKey())

	// For each packdata, wrap for new readers.
	for j, d := range packdata {

		// Extract dkey and existing wrapped keys from packdata.
		var dkey []byte
		alreadyWrapped := make(map[keyHashArray]*wrappedKey)
		sig, sig2, wrap, cipherSum, err := ee.pdUnmarshal(*d)
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
			dkey, err = ee.aesUnwrap(ctx.Factotum, w)
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
				w, err := ee.aesWrap(pubkey[i], dkey)
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
		dst := make([]byte, ee.packdataLen(nwrap))
		if ee.pdMarshal(&dst, sig, sig2, wrap, cipherSum) != nil {
			packdata[j] = nil // Tell caller this packdata was skipped.
		} else {
			*packdata[j] = dst
		}
	}
}

// Name implements upspin.Name.
func (ee ee) Name(ctx *upspin.Context, d *upspin.DirEntry, newName upspin.PathName) error {
	const Name = "Name"
	if d.IsDir() {
		return errors.E(Name, d.Name, errors.IsDir, "cannot rename directory")
	}
	if err := pack.CheckUnpackMeta(ee, &d.Metadata); err != nil {
		return errors.E(Name, errors.Invalid, d.Name, err)
	}

	dkey := make([]byte, aesKeyLen)
	sig, sig2, wrap, cipherSum, err := ee.pdUnmarshal(d.Metadata.Packdata)
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
	ownerPubKey, _, err := factotum.ParsePublicKey(ownerRawPubKey)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}

	// Now get my own keys
	me := ctx.UserName // Recipient of the file is me (the user in the context)
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
	dkey, err = ee.aesUnwrap(ctx.Factotum, w)
	if err != nil {
		log.Printf("unwrap failed: %s", err)
		return errors.E(Name, d.Name, errors.Str("unwrap failed"))
	}

	// Verify that this was signed with the owner's old or new public key.
	vhash := factotum.VerHash(ctx.Packing, path.Clean(d.Name), d.Metadata.Time, dkey, cipherSum)
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
	sig, err = ctx.Factotum.FileSign(ctx.Packing, newName, d.Metadata.Time, dkey, cipherSum)
	if err != nil {
		return errors.E(Name, d.Name, err)
	}

	// Serialize packer metadata. We do not reallocate Packdata since the new data
	// should be the same size or smaller.
	if err := ee.pdMarshal(&d.Metadata.Packdata, sig, sig0, wrap, cipherSum); err != nil {
		return errors.E(Name, d.Name, err)
	}
	d.Name = newName

	return nil
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func (ee ee) aesWrap(R *ecdsa.PublicKey, dkey []byte) (w wrappedKey, err error) {
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

func (ee ee) pdMarshal(dst *[]byte, sig, sig2 upspin.Signature, wrap []wrappedKey, cipherSum []byte) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation
	n := ee.packdataLen(len(wrap))
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
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
	// TODO(p): eventually make hash mandatory.  Currently not for backward compatability.
	if cipherSum != nil {
		n += pdPutBytes((*dst)[n:], cipherSum)
	}
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

func (ee ee) pdUnmarshal(pd []byte) (sig, sig2 upspin.Signature, wrap []wrappedKey, hash []byte, err error) {
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

// pdBytes copies (part of) src to dst, based on length header; returns bytes consumed
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

func (ee ee) encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != aesKeyLen {
		return 0, errKeyLength
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return 0, err
	}
	iv := make([]byte, aes.BlockSize)
	// iv=0 is ok because we're CERTAIN that dkey is random and not reused
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, cleartext)
	return len(cleartext), nil
}

func (ee ee) decrypt(cleartext, ciphertext, dkey []byte) (int, error) {
	if len(dkey) != aesKeyLen {
		return 0, errKeyLength
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return 0, err
	}
	iv := make([]byte, aes.BlockSize)
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(cleartext, ciphertext)
	return len(ciphertext), nil
}

// packdataLen returns n big enough for packing, sig.R, sig.S, nwrap, {keyHash, encrypted, nonce, X, y}
func (ee ee) packdataLen(nwrap int) int {
	return 1 + 2*marshalBufLen + (1+5*nwrap)*binary.MaxVarintLen64 +
		nwrap*(sha256.Size+(aesKeyLen+gcmTagSize)+gcmStandardNonceSize+2*marshalBufLen) +
		sha256.Size + 1
}

// publicKey returns the string representation of a user's public key.
func publicKey(ctx *upspin.Context, user upspin.UserName) (upspin.PublicKey, error) {

	// Key pairs have three representations:
	// 1. string, used for storage and between programs like User.Lookup
	// 2. ecdsa, internal binary format for computation
	// 3. a secret seed sufficient to reconstruct the key pair
	// In form 1, the first bytes describe the packing name, e.g. "p256".
	// In form 2, there is an Curve field in the struct that plays that role.
	// Form 3, used only in keygen.go, is simply 128 bits of entropy.

	log.Debug.Printf("Getting pub key for user: %s", user) // TODO(ehg) Log no longer needed?
	// Are we requesting our own public key?
	if string(user) == string(ctx.UserName) {
		return ctx.Factotum.PublicKey(), nil
	}
	userService, err := bind.User(ctx, ctx.UserEndpoint)
	if err != nil {
		return "", err
	}
	_, keys, err := userService.Lookup(user)
	if err != nil {
		return "", err
	}
	if len(keys) < 1 {
		return "", errors.E(user, errors.NotExist, errNoKnownKeysForUser)
	}
	return keys[0], nil
}
