// Package cache implements various caching strategies.
package cache

// This is copied verbatim from https://github.com/golang/build/blob/master/internal/lru/cache.go

import (
	"container/list"
	"sync"
)

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

// NewLRUCache returns a new cache with the provided maximum items.
func NewLRUCache(maxEntries int) *LRU {
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
		c.removeOldest()
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

// RemoveOldest removes the oldest item in the cache and returns its key and value.
// If the cache is empty, the empty string and nil are returned.
func (c *LRU) RemoveOldest() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeOldest()
}

// note: must hold c.mu
func (c *LRU) removeOldest() (key, value interface{}) {
	ele := c.ll.Back()
	if ele == nil {
		return
	}
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
