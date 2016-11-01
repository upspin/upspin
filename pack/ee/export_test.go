// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee

import (
	"crypto/cipher"

	"upspin.io/upspin"
)

// Exported for testing only.
func NewKeyAndCipher() ([]byte, cipher.Block, error) {
	return newKeyAndCipher()
}

func SetblockPacker(b upspin.BlockPacker, dkey []byte, cipher cipher.Block) {
	bp := b.(*blockPacker)
	bp.dkey = dkey
	bp.cipher = cipher
}
