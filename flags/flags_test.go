// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flags

import (
	"fmt"
	"testing"

	"upspin.io/upspin"
)

func TestMaxBlockSize(t *testing.T) {
	defer func() {
		blockSize = defaultBlockSize
		BlockSize = defaultBlockSize
	}()
	sizes := []int64{-1, 0, 1234, 1000000000, 1100000000}
	for _, size := range sizes {
		shouldErr := size <= 0 || upspin.MaxBlockSize < size
		err := blockSize.Set(fmt.Sprint(size))
		if shouldErr {
			if err == nil {
				t.Errorf("expected error for %d; got none", size)
			}
			continue
		}
		if err != nil {
			t.Errorf("expected no error for %d; got %v", size, err)
			continue
		}
		if int64(BlockSize) != size {
			t.Errorf("BlockSize is %d; want %d", BlockSize, size)
		}
	}
}
