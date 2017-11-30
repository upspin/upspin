// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package packutil provides helper functions for DirEntry Packdata computation.
package packutil // import "upspin.io/pack/packutil"

import (
	"encoding/binary"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// PutBytes stores the varint-encoded length of src in dst, followed by a copy of src.
// It returns the number of bytes written to dst.
func PutBytes(dst, src []byte) int {
	vlen := binary.PutVarint(dst, int64(len(src)))
	return vlen + copy(dst[vlen:], src)
}

// GetBytes copies (part of) src to dst, based on a length header.
// It returns the number of bytes consumed, including the header.
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
		return "", errors.E(user, errors.NotExist, "no known keys for user")
	}
	return u.PublicKey, nil
}
