// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"testing"
	"time"
)

func TestGate(t *testing.T) {
	g := Gate{
		Backoff: 10 * time.Second,
		Max:     99 * time.Second,
	}

	now := time.Date(2017, time.January, 1, 0, 0, 0, 0, time.UTC)

	const (
		a, b = "a", "b"
	)
	testCases := []struct {
		key  string
		sec  int
		want bool
		len  int
	}{
		{a, 0, true, 1}, // backoff 10s

		{a, 1, false, 1},
		{a, 9, false, 1},
		{a, 10, false, 1},
		{a, 11, true, 1}, // backoff 20s

		{b, 15, true, 2},

		{a, 22, false, 2},
		{a, 31, false, 2},
		{a, 32, true, 2}, // backoff 40s

		{b, 40, true, 2},

		{a, 200, true, 1}, // backoff 10s

		{a, 210, false, 1},
		{a, 211, true, 1}, // backoff 20s

		{a, 320, true, 1}, // backoff 10s
	}
	for _, c := range testCases {
		got := g.pass(now.Add(time.Duration(c.sec)*time.Second), c.key)
		if got != c.want {
			t.Errorf("%d seconds for %q: got %v, want %v", c.sec, c.key, got, c.want)
		}
		if got, want := len(g.s), c.len; got != want {
			t.Errorf("%d seconds for %q: len(g.s) = %d, want %d", c.sec, c.key, got, want)
		}
		if got, want := len(g.m), c.len; got != want {
			t.Errorf("%d seconds for %q: len(g.m) = %d, want %d", c.sec, c.key, got, want)
		}
	}
}
