// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import "upspin.io/upspin"

// NewBlockTracker returns a BlockTracker that iterates over the given slice.
func NewBlockTracker(bs []upspin.DirBlock) BlockTracker {
	return BlockTracker{bs: bs, Block: -1}
}

// BlockTracker maintains an index into a slice of upspin.DirBlock.
type BlockTracker struct {
	// Block is the index of the current block.
	Block int

	bs []upspin.DirBlock
}

// NextBlock implements part of the upspin.BlockUnpacker interface.
func (t *BlockTracker) NextBlock() (upspin.DirBlock, bool) {
	t.Block++
	if t.Block >= len(t.bs) {
		return upspin.DirBlock{}, false
	}
	return t.bs[t.Block], true
}

// SeekBlock implements part of the upspin.BlockUnpacker interface.
func (t *BlockTracker) SeekBlock(n int) (upspin.DirBlock, bool) {
	if n < 0 || n >= len(t.bs) {
		return upspin.DirBlock{}, false
	}
	t.Block = n
	return t.bs[t.Block], true
}
