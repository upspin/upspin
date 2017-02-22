// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal // import "upspin.io/pack/internal"

import (
	"crypto/sha256"

	"upspin.io/upspin"
)

// BlockSum returns the SHA256 hash of the given DirBlocks' Packdata.
func BlockSum(bs []upspin.DirBlock) []byte {
	hash := sha256.New()
	for i := range bs {
		hash.Write(bs[i].Packdata)
	}
	return hash.Sum(nil)
}
