// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"upspin.io/errors"
	"upspin.io/pack/packutil"
	"upspin.io/upspin"
)

// wrappedKey encodes a key that will decrypt and verify the ciphertext.
type wrappedKey struct {
	keyHash   []byte // recipient's public key
	dkey      []byte // ciphertext symmetric decryption key
	nonce     []byte
	ephemeral ecdsa.PublicKey
}

// packdata is a structured representation of the DirEntry's Packdata field.
type packdata struct {
	// sig is the signature with the primary owner key.
	sig upspin.Signature
	// sig2 is the signature with the previous owner key,
	// to enable smoother key rotation.
	sig2 upspin.Signature
	// wrap is the file key, encoded with a set of reader keys.
	wrap []wrappedKey
	// blockSum is a checksum of the blocks.
	blockSum []byte
}

// Marshal stores the binary-encoded version of packdata in the given slice,
// copying byte arrays to dst in the order declared in the struct definitions
// and prefixed with lengths using binary.PutVarint.
// A slice will be allocated and the pointer overwritten if *dst is too short.
func (pd *packdata) Marshal(dst *[]byte) error {
	if n := packdataLen(len(pd.wrap)); len(*dst) < n {
		*dst = make([]byte, n)
	}

	n := 0

	// sig
	n += packutil.PutBytes((*dst)[n:], pd.sig.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], pd.sig.S.Bytes())

	// sig2
	sig2 := pd.sig2
	if sig2.R == nil {
		zero := big.NewInt(0)
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += packutil.PutBytes((*dst)[n:], sig2.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig2.S.Bytes())

	// wrap
	n += binary.PutVarint((*dst)[n:], int64(len(pd.wrap)))
	for _, w := range pd.wrap {
		n += packutil.PutBytes((*dst)[n:], w.keyHash)
		n += packutil.PutBytes((*dst)[n:], w.dkey)
		n += packutil.PutBytes((*dst)[n:], w.nonce)
		if w.ephemeral.X != nil {
			n += packutil.PutBytes((*dst)[n:], w.ephemeral.X.Bytes())
		} else {
			n += packutil.PutBytes((*dst)[n:], nil)
		}
		if w.ephemeral.Y != nil {
			n += packutil.PutBytes((*dst)[n:], w.ephemeral.Y.Bytes())
		} else {
			n += packutil.PutBytes((*dst)[n:], nil)
		}
	}

	// blockSum
	n += packutil.PutBytes((*dst)[n:], pd.blockSum)

	*dst = (*dst)[:n]
	return nil
}

// Unmarshal parses the given packdata slice and stores its contents in the
// receiver pd.
func (pd *packdata) Unmarshal(b []byte) error {
	if len(b) == 0 {
		return errors.Str("nil packdata")
	}
	n := 0

	// sig
	pd.sig.R = big.NewInt(0)
	pd.sig.S = big.NewInt(0)
	buf := make([]byte, marshalBufLen)
	n += packutil.GetBytes(&buf, b[n:])
	pd.sig.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, b[n:])
	pd.sig.S.SetBytes(buf)

	// sig2
	pd.sig2.R = big.NewInt(0)
	pd.sig2.S = big.NewInt(0)
	n += packutil.GetBytes(&buf, b[n:])
	pd.sig2.R.SetBytes(buf)
	n += packutil.GetBytes(&buf, b[n:])
	pd.sig2.S.SetBytes(buf)

	// wrap
	nwrap64, vlen := binary.Varint(b[n:])
	n += vlen
	nwrap := int(nwrap64)
	if int64(nwrap) != nwrap64 {
		return errors.Errorf("implausible number of wrapped keys: %d\n", nwrap64)
	}
	pd.wrap = make([]wrappedKey, nwrap)
	for i := 0; i < nwrap; i++ {
		var w wrappedKey
		w.keyHash = make([]byte, sha256.Size)
		w.dkey = make([]byte, aesKeyLen+gcmTagSize)
		w.nonce = make([]byte, gcmStandardNonceSize)
		w.ephemeral = ecdsa.PublicKey{X: big.NewInt(0), Y: big.NewInt(0)}
		n += packutil.GetBytes(&w.keyHash, b[n:])
		n += packutil.GetBytes(&w.dkey, b[n:])
		n += packutil.GetBytes(&w.nonce, b[n:])
		n += packutil.GetBytes(&buf, b[n:])
		w.ephemeral.X.SetBytes(buf)
		n += packutil.GetBytes(&buf, b[n:])
		w.ephemeral.Y.SetBytes(buf)
		if w.ephemeral.Y.BitLen() > 393 {
			w.ephemeral.Curve = elliptic.P521()
		} else if w.ephemeral.Y.BitLen() > 265 {
			w.ephemeral.Curve = elliptic.P384()
		} else {
			w.ephemeral.Curve = elliptic.P256()
		}
		pd.wrap[i] = w
	}

	// blockSum
	pd.blockSum = make([]byte, sha256.Size)
	n += packutil.GetBytes(&pd.blockSum, b[n:])
	if pd.blockSum == nil {
		return errors.Str("block checksum is required")
	}

	return nil
}

// packdataLen returns the maximum length of a packdata slice for the given
// number of wrapped keys.
func packdataLen(nwrap int) int {
	intLen := binary.MaxVarintLen64

	// nWrappedKey is the size of a single encoded wrappedKey
	nWrappedKey := intLen + sha256.Size            // keyHash
	nWrappedKey += intLen + aesKeyLen + gcmTagSize // dkey
	nWrappedKey += intLen + gcmStandardNonceSize   // nonce
	nWrappedKey += 2 * (intLen + marshalBufLen)    // ephemeral

	n := 4 * (intLen + marshalBufLen) // (R,S) for (sig, sig2)
	n += intLen                       // len(wrap)
	n += nwrap * nWrappedKey
	n += intLen + sha256.Size // blockSum

	// n is commonly an overestimate since the big.Int used in p256 are
	// about half the size of big.Int used in the assumed curve p521.
	// At the time of writing:
	//   marshalBufLen=66    curve.Params().BitSize + 7) >> 3 for p521
	//   MaxVarintLen64=10
	//   sha256.Size=32
	//   aesKeyLen=32
	//   gcmTagSize=16
	//   gcmStandardNonceSize=12
	// and therefore n = 356 + nwrap*274.
	// On a 32-bit machine, this supports well over a million readers.
	// We would redesign to use group keys long before that.
	return n
}
