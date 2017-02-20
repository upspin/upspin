// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache_test

// This copied and minimally adapted from: https://github.com/golang/build/blob/master/internal/lru/cache_test.go

// Copyright 2011 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import (
	"reflect"
	"testing"

	"upspin.io/cache"
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

func TestPeek(t *testing.T) {
	c := cache.NewLRU(2)

	if k, v := c.PeekOldest(); k != nil || v != nil {
		t.Errorf("LRU = %q, %q; want nil, nil", k, v)
	}
	if k, v := c.PeekNewest(); k != nil || v != nil {
		t.Errorf("MRU = %q, %q; want nil, nil", k, v)
	}

	c.Add("k1", "v1")
	c.Add("k2", "v2")

	if k, v := c.PeekOldest(); k != "k1" || v != "v1" {
		t.Errorf("LRU = %q, %q; want k1, v1", k, v)
	}
	if k, v := c.PeekNewest(); k != "k2" || v != "v2" {
		t.Errorf("MRU = %q, %q; want k2, v2", k, v)
	}

	c.Get("k1")
	if k, v := c.PeekOldest(); k != "k2" || v != "v2" {
		t.Errorf("LRU = %q, %q; want k2, v2", k, v)
	}
	if k, v := c.PeekNewest(); k != "k1" || v != "v1" {
		t.Errorf("MRU = %q, %q; want k1, v1", k, v)
	}

	c.Add("k3", "v3")
	if k, v := c.PeekOldest(); k != "k1" || v != "v1" {
		t.Errorf("MRU = %q, %q; want k1, v1", k, v)
	}
	if k, v := c.PeekNewest(); k != "k3" || v != "v3" {
		t.Errorf("MRU = %q, %q; want k3, v3", k, v)
	}
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

func TestRemoveOne(t *testing.T) {
	c := cache.NewLRU(10)
	c.Add("1", "one")
	c.Add("2", "two")
	if c.Len() != 2 {
		t.Errorf("Expected Len=2, got %d", c.Len())
	}
	value := c.Remove("2")
	if value != "two" {
		t.Errorf("Expected 'two', got %v", value)
	}
	if c.Len() != 1 {
		t.Errorf("Expected Len=1, got %d", c.Len())
	}
	value = c.Remove("99")
	if value != nil {
		t.Errorf("Expected nil")
	}
}

type testOnEviction struct {
	keyDeleted string
}

func (t *testOnEviction) OnEviction(key interface{}) {
	t.keyDeleted = key.(string)
}

func TestEvictionNotifier(t *testing.T) {
	c := cache.NewLRU(1)
	one := &testOnEviction{}
	two := &testOnEviction{}
	three := &testOnEviction{}

	c.Add("1", one)
	c.Add("2", two)
	c.Add("3", three)

	if one.keyDeleted != "1" {
		t.Errorf("keyCalled = %s, want = 1", one.keyDeleted)
	}
	if two.keyDeleted != "2" {
		t.Errorf("keyCalled = %s, want = 2", two.keyDeleted)
	}
	c.RemoveOldest()
	if three.keyDeleted != "" {
		t.Errorf("Called OnEviction from RemoveOldest")
	}

	four := &testOnEviction{}
	c.Add("4", four)
	c.Remove("4")
	if four.keyDeleted != "" {
		t.Errorf("Called OnEviction from Remove")
	}

	if c.Len() != 0 {
		t.Errorf("Expected len = 0, got = %d", c.Len())
	}
}
