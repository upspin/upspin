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

// Marshal stores the binary-encoded version of packdata in the given slice.
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
		n += packutil.PutBytes((*dst)[n:], w.ephemeral.X.Bytes())
		n += packutil.PutBytes((*dst)[n:], w.ephemeral.Y.Bytes())
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
	// TODO(grosse): explain the components of this expression.
	return 2*marshalBufLen + (1+5*nwrap)*binary.MaxVarintLen64 +
		nwrap*(sha256.Size+(aesKeyLen+gcmTagSize)+gcmStandardNonceSize+2*marshalBufLen) +
		sha256.Size + 1
}
