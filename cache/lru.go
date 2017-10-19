// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cache implements various caching strategies.
package cache // import "upspin.io/cache"

// This is mostly copied from https://github.com/golang/build/blob/master/internal/lru/cache.go

import (
	"container/list"
	"sync"
)

// EvictionNotifier is an optional interface used by LRU entries that wish to be
// notified when they are being deleted from the LRU. If implemented by the
// value of an LRU entry, OnEviction is called when it's time for that
// entry to be evicted, due to an Add. It is not called if the entry is removed
// by the LRU's Remove or RemoveOldest methods.
type EvictionNotifier interface {
	// OnEviction is called on the value of an LRU entry when it's about
	// to be evicted from the cache. This method must not call the LRU
	// cache nor block indefinitely.
	OnEviction(key interface{})
}

// LRU is a least-recently used cache, safe for concurrent access.
type LRU struct {
	maxEntries int

	mu    sync.Mutex
	ll    *list.List
	cache map[interface{}]*list.Element
}

// *entry is the type stored in each *list.Element.
type entry struct {
	key, value interface{}
}

const runEvictionNotifier = true

// NewLRU returns a new cache with the provided maximum items.
func NewLRU(maxEntries int) *LRU {
	return &LRU{
		maxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[interface{}]*list.Element),
	}
}

// Add adds the provided key and value to the cache, evicting
// an old item if necessary.
func (c *LRU) Add(key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Already in cache?
	if ee, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ee)
		ee.Value.(*entry).value = value
		return
	}

	// Add to cache if not present
	ele := c.ll.PushFront(&entry{key, value})
	c.cache[key] = ele

	if c.ll.Len() > c.maxEntries {
		c.removeOldest(runEvictionNotifier)
	}
}

// Get fetches the key's value from the cache.
// The ok result will be true if the item was found.
func (c *LRU) Get(key interface{}) (value interface{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry).value, true
	}
	return
}

// RemoveOldest removes the oldest item in the cache and returns its key and
// value. If the cache is empty, the empty string and nil are returned. The
// value's EvictionNotifier is not run.
func (c *LRU) RemoveOldest() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeOldest(!runEvictionNotifier)
}

// Remove removes a key from the cache. The return value is the value that was
// removed or nil if the key was not present. The value's EvictionNotifier is
// not run by Remove.
func (c *LRU) Remove(key interface{}) interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, found := c.cache[key]; found {
		_, value := c.remove(ele)
		return value
	}
	return nil
}

// note: must hold c.mu
func (c *LRU) removeOldest(runEvictionNotifier bool) (key, value interface{}) {
	ele := c.ll.Back()
	if ele == nil {
		return
	}
	if runEvictionNotifier {
		// If EvictionNotifier interface is implemented on the value,
		// run it.
		ent := ele.Value.(*entry)
		if notifier, ok := ent.value.(EvictionNotifier); ok {
			notifier.OnEviction(ent.key)
		}
	}
	return c.remove(ele)
}

// note: must hold c.mu
func (c *LRU) remove(ele *list.Element) (key, value interface{}) {
	c.ll.Remove(ele)
	ent := ele.Value.(*entry)
	delete(c.cache, ent.key)
	return ent.key, ent.value
}

// Len returns the number of items in the cache.
func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// PeekOldest peeks at the least-recently used entry, without modifying the
// cache in any way. If the cache is empty, two nils are returned.
func (c *LRU) PeekOldest() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ele := c.ll.Back()
	if ele == nil {
		return
	}
	ent := ele.Value.(*entry)
	return ent.key, ent.value
}

// PeekNewest peeks at the most-recently used entry, without modifying the cache
// in any way. If the cache is empty, two nils are returned.
func (c *LRU) PeekNewest() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ele := c.ll.Front()
	if ele == nil {
		return
	}
	ent := ele.Value.(*entry)
	return ent.key, ent.value
}

// Iterator is an iterator through the list. The iterator points to a nil
// element at the end of the list.
type Iterator struct {
	lru     *LRU
	this    *list.Element
	forward bool
}

// NewIterator returns a new iterator for the LRU.
func (c *LRU) NewIterator() *Iterator {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &Iterator{lru: c, this: c.ll.Front(), forward: true}
}

// NewReverseIterator returns a new reverse iterator for the LRU.
func (c *LRU) NewReverseIterator() *Iterator {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &Iterator{lru: c, this: c.ll.Back(), forward: false}
}

// GetAndAdvance returns key, value, true if the current entry is valid and advances the
// iterator. Otherwise it returns nil, nil, false.
func (i *Iterator) GetAndAdvance() (interface{}, interface{}, bool) {
	i.lru.mu.Lock()
	defer i.lru.mu.Unlock()
	if i.this == nil {
		return nil, nil, false
	}
	ent := i.this.Value.(*entry)
	if i.forward {
		i.this = i.this.Next()
	} else {
		i.this = i.this.Prev()
	}
	return ent.key, ent.value, true
}
