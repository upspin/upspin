// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"testing"
)

func BenchmarkDirEntryMarshalEmptyBlocks(b *testing.B) {
	// linkDirEnt has a link and no blocks.
	benchmarkDirEntryMarshal(linkDirEnt, b)
}

func BenchmarkDirEntryMarshal2Blocks(b *testing.B) {
	// dirEnt has 2 blocks.
	benchmarkDirEntryMarshal(dirEnt, b)
}

func benchmarkDirEntryMarshal(de DirEntry, b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := de.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
}
