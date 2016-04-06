package cache_test

// This copied and minimally adapted from: https://github.com/golang/build/blob/master/internal/lru/cache_test.go

// Copyright 2011 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import (
	"reflect"
	"testing"

	"upspin.googlesource.com/upspin.git/cache"
)

func TestLRU(t *testing.T) {
	c := cache.NewLRU(2)

	expectMiss := func(k string) {
		v, ok := c.Get(k)
		if ok {
			t.Fatalf("expected cache miss on key %q but hit value %v", k, v)
		}
	}

	expectHit := func(k string, ev interface{}) {
		v, ok := c.Get(k)
		if !ok {
			t.Fatalf("expected cache(%q)=%v; but missed", k, ev)
		}
		if !reflect.DeepEqual(v, ev) {
			t.Fatalf("expected cache(%q)=%v; but got %v", k, ev, v)
		}
	}

	expectMiss("1")
	c.Add("1", "one")
	expectHit("1", "one")

	c.Add("2", "two")
	expectHit("1", "one")
	expectHit("2", "two")

	c.Add("3", "three")
	expectHit("3", "three")
	expectHit("2", "two")
	expectMiss("1")
}

func TestRemoveOldest(t *testing.T) {
	c := cache.NewLRU(2)
	c.Add("1", "one")
	c.Add("2", "two")
	if k, v := c.RemoveOldest(); k != "1" || v != "one" {
		t.Fatalf("oldest = %q, %q; want 1, one", k, v)
	}
	if k, v := c.RemoveOldest(); k != "2" || v != "two" {
		t.Fatalf("oldest = %q, %q; want 2, two", k, v)
	}
	if k, v := c.RemoveOldest(); k != nil || v != nil {
		t.Fatalf("oldest = %v, %v; want \"\", nil", k, v)
	}
}
