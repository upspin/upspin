// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"fmt"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	r := RateLimiter{
		Backoff: 10 * time.Second,
		Max:     99 * time.Second,
	}

	now := time.Date(2017, time.January, 1, 0, 0, 0, 0, time.UTC)

	const (
		a, b = "a", "b"
	)
	testCases := []struct {
		key        string
		sec        int
		pass       bool
		backoffSec int
		len        int
	}{
		{a, 0, true, 0, 1}, // backoff 10s

		{a, 1, false, 9, 1},
		{a, 9, false, 1, 1},
		{a, 10, false, 0, 1},
		{a, 11, true, 0, 1}, // backoff 20s

		{b, 15, true, 0, 2},
		{"c", 24, true, 0, 3},
		{"d", 31, true, 0, 4},

		{a, 22, false, 9, 4},
		{a, 31, false, 0, 4},
		{a, 32, true, 0, 4}, // backoff 40s

		{b, 40, true, 0, 4},

		{a, 200, true, 0, 1}, // backoff 10s

		{a, 210, false, 0, 1},
		{a, 211, true, 0, 1}, // backoff 20s

		{a, 320, true, 0, 1}, // backoff 10s
	}
	for i, c := range testCases {
		pass, backoff := r.pass(now.Add(time.Duration(c.sec)*time.Second), c.key)
		if pass != c.pass {
			t.Errorf("case %d: %d seconds for %q: got %v, want %v", i, c.sec, c.key, pass, c.pass)
		}
		if backoff != time.Duration(c.backoffSec)*time.Second {
			t.Errorf("case %d: expected backoff = %d s, got = %v", i, c.backoffSec, backoff)
		}
		if got, want := len(r.m), c.len; got != want {
			t.Errorf("case %d: %d seconds for %q: len(r.m) = %d, want %d", i, c.sec, c.key, got, want)
		}
	}

	// Test purging of old visitors.
	for i := 0; i < rateMaxVisitors+1; i++ {
		now = now.Add(time.Nanosecond)
		r.pass(now, fmt.Sprint(i))
	}
	if ok, _ := r.pass(now, "0"); !ok {
		t.Errorf("key 0 should have been purged")
	}
	k := fmt.Sprint(rateMaxVisitors)
	if ok, _ := r.pass(now, k); ok {
		t.Errorf("key %v should not have been purged", k)
	}
}

func BenchmarkRateLimiter(b *testing.B) {
	r := RateLimiter{
		Backoff: 10 * time.Second,
		Max:     99 * time.Second,
	}

	now := time.Now()
	for n := 0; n < b.N; n++ {
		now = now.Add(time.Nanosecond)
		r.pass(now, fmt.Sprint(n))
	}
}
