// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sha256key provides access to the hash function used to make content-addressable keys.
package sha256key // import "upspin.io/key/sha256key"

import (
	"crypto/sha256"
	"fmt"

	"upspin.io/errors"
)

var (
	errHashFormat = errors.Str("bad hash format")
)

// Size is the number of bytes in a hash.
const Size = sha256.Size

// ZeroHash is the zero-valued hash.
var ZeroHash Hash

// Hash represents a SHA-256 hash code. It is always 32 bytes long.
// Its representation is an array so it can be treated as a value.
type Hash [Size]byte // SHA-256 hash always 32 bytes

// String returns a hexadecimal representation of the hash.
func (hash Hash) String() string {
	return BytesString(hash[:])
}

// BytesString returns a string representation of the hash that is represented in bytes.
func BytesString(hash []byte) string {
	return fmt.Sprintf("%X", hash)
}

// EqualString compares the byte-level representation of a hash with its hex string representation,
// avoiding allocation.
func (hash Hash) EqualString(b string) bool {
	h, err := Parse(b)
	return err == nil && h == hash
}

// Parse returns the hash whose standard format (possibly absent the brackets) is the value of str.
func Parse(str string) (hash Hash, err error) {
	err = errHashFormat
	if len(str) != 2*Size {
		return
	}
	for i := range hash {
		a := unhex(str[2*i])
		b := unhex(str[2*i+1])
		if a == 255 || b == 255 {
			return
		}
		hash[i] = a<<4 | b
	}
	err = nil
	return
}

// unhex returns the value of the hex nibble or 255 if it's bad.
func unhex(b uint8) uint8 {
	switch {
	case '0' <= b && b <= '9':
		return b - '0'
	case 'a' <= b && b <= 'f':
		return 10 + b - 'a'
	case 'A' <= b && b <= 'F':
		return 10 + b - 'A'
	}
	return 255
}

// Of returns the SHA-256 hash of the data, as a Hash.
// The odd name works well in the client: sha256key.Of.
func Of(data []byte) (hash Hash) {
	return sha256.Sum256(data)
}
