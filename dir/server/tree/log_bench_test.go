// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"testing"
)

func BenchmarkLogEntryMarshal(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := entry.marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
}
