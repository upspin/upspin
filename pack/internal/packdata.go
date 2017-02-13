// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"encoding/binary"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

var errNoKnownKeysForUser = errors.Str("no known keys for user")

// PutBytes puts length header in dst and then copies src to dst; returns bytes consumed
func PutBytes(dst, src []byte) int {
	vlen := binary.PutVarint(dst, int64(len(src)))
	return vlen + copy(dst[vlen:], src)
}

// GetBytes copies (part of) src to dst, based on length header; returns bytes consumed
func GetBytes(dst *[]byte, src []byte) int {
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

// GetPublicKey returns the string representation of a user's public key.
func GetPublicKey(cfg upspin.Config, user upspin.UserName) (upspin.PublicKey, error) {

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
