// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storecache

import (
	"testing"
)

func TestParallelismOK(t *testing.T) {
	max := 5
	p := newParallelism(max)
	for i := 0; i < max; i++ {
		if !p.ok() {
			t.Errorf("added %d: p.ok=%v, want %v", i, p.ok(), true)
		} else {
			p.add()
		}
	}

	if p.ok() {
		t.Errorf("added %d: p.ok=%v, want %v", max, p.ok(), false)
	}
}

func TestParallelismSuccess(t *testing.T) {
	max := 5
	p := newParallelism(max)

	// fill in inflights with write load
	for i := 0; i < max; i++ {
		p.add()
	}

	for n := 1; n <= 3; n++ {
		// start to succeed with continuous write load
		for i := 0; i < max+n; i++ {
			p.success()
			p.add()
		}

		if p.max != max+n {
			t.Errorf("p.max = %d, want %d", p.max, max+n)
		}

		// fill in the one new slot of inflight
		p.add()
	}
}
