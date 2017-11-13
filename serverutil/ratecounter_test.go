// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"testing"
	"time"
)

func TestRateCounter(t *testing.T) {
	ready := make(chan bool)
	onReady = func() {
		ready <- true
	}
	defer func() {
		onReady = func() {}
	}()

	tick := make(chan time.Time)
	rc := newRateCounter(3, time.Second, tick)

	for _, c := range []struct {
		add  int64
		want float64
	}{
		{3, 1.0}, // First second. [3, 0, 0] rate = 1
		{6, 3.0}, // Second second. [3, 6, 0] rate = 3
		{0, 3.0}, // Third second. [3, 6, 0] rate = 3
		{0, 2.0}, // Fourth second. [0, 6, 0] rate = 2
		{0, 0.0}, // Fifth second. [0, 0, 0] rate = 0
	} {
		rc.Add(c.add)
		if got, want := rc.Rate(), c.want; got != want {
			t.Errorf("rate = %f, want = %f", got, want)
		}
		tick <- time.Now()
		<-ready
	}

	rc.Add(12)
	if rc.String() != `"4 ops/s"` {
		t.Errorf("got = %s", rc)
	}
}
