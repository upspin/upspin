// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storecacheserver is a caching proxy between a client and all stores.
package storecacheserver

import (
	"errors"
	"io"
	"os"
	"sync"

	"upspin.io/cache"
	"upspin.io/log"
	"upspin.io/upspin"
)

// In the following code to avoid deadlock always lock in the order
//   lruLock -> cachedRef -> inUseLock

// cachedRef represents a cached reference.
type cachedRef struct {
	sync.Mutex
	c     *storeCache
	ref   upspin.Reference
	size  int64
	store upspin.Endpoint // Store reference belongs to (not yet used).
	busy  bool            // True if the ref is in the process of being cached.
	hold  *sync.Cond      // Wait here if some other func is caching the ref.
	valid bool            // True if successfully cached.
}

// cache represents a Write through cache for references. If, upon adding to the cache,
// we find more than limit bytes in use, we will remove the oldest entry until below
// the limit. It is possible to push past the limit; it is sofr, not a hard limit.
//
// TODO(p): make this a write back cache to allow disconnected operation.
type storeCache struct {
	sync.Mutex
	dir       string     // Top directory for cached references.
	limit     int64      // Maximum bytes (more or less) to store.
	lru       *cache.LRU // Key is the reference. Value is &cachedRef.
	inUseLock sync.Mutex
	inUse     int64 // Current bytes cached.
}

// newCache returns a newly created cache rooted at dir.
func newCache(dir string, maxBytes int64, maxRefs int) *storeCache {
	os.MkdirAll(dir, 0700)
	return &storeCache{dir: dir, limit: maxBytes, lru: cache.NewLRU(maxRefs)}
}

// cacheName builds a path to the local cachefile from all the Locations making up the file.
// It returns paths to both the containing directory and the file itself.
func (c *storeCache) cacheName(ref upspin.Reference) (string, string) {
	dir := c.dir + "/" + string(ref[:2])
	file := dir + "/" + string(ref)
	return dir, file
}

// enforce removes the oldest entries until inUse is below limit.
func (c *storeCache) enforce() {
	for {
		c.Lock()
		c.inUseLock.Lock()
		if c.inUse < c.limit {
			c.inUseLock.Unlock()
			c.Unlock()
		}
		key, value := c.lru.RemoveOldest()
		c.inUse -= value.(*cachedRef).size
		c.inUseLock.Unlock()
		c.Unlock()
		_, name := c.cacheName(key.(upspin.Reference))
		os.Remove(name)
	}
}

// Since the LRU is fixed size, make sure we clean up after evictions.
// Called with cr locked.
func (cr *cachedRef) OnEviction(key interface{}) {
	cr.valid = false
	_, name := cr.c.cacheName(key.(upspin.Reference))
	os.Remove(name)
}

// newCachedRef creates a new locked and busy cachedRef.
// Called with storeCache.lruLock locked.
func (c *storeCache) newCachedRef(ref upspin.Reference) *cachedRef {
	cr := &cachedRef{busy: true, c: c}
	cr.hold = sync.NewCond(cr)
	cr.Lock()
	c.lru.Add(ref, cr)
	return cr
}

// readFromCachefile reads in the cache file, if it exists.
// Called with the cachedFile locked.
func readFromCacheFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	buf := make([]byte, info.Size())
	n, err := f.Read(buf)
	f.Close()
	if err != nil {
		if err != io.EOF {
			return nil, err
		}
		buf = buf[:n]
	}
	return buf, nil
}

// saveToCacheFile saves a ref in the cache.
// Called with cr locked.
func (cr *cachedRef) saveToCacheFile(dir, name string, data []byte) error {
	tmpName := name + ".tmp"
	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		os.Mkdir(dir, 0700)
		f, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	n, err := f.Write(data)
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	if n != len(data) {
		os.Remove(tmpName)
		return errors.New("writing cache file")
	}
	if err := os.Rename(tmpName, name); err != nil {
		os.Remove(tmpName)
		return err
	}
	cr.size = int64(len(data))
	cr.valid = true

	// Update the total bytes cached.
	cr.c.inUseLock.Lock()
	cr.c.inUse += cr.size
	cr.c.inUseLock.Unlock()
	return nil
}

// get fetches a reference.
func (c *storeCache) get(ref upspin.Reference, store upspin.StoreServer) ([]byte, []upspin.Location, error) {
	dir, name := c.cacheName(ref)
	c.enforce()

	// Loop trying to get the reference from the cache. We terminate when we either have
	// gotten the reference or noone else is in the process of getting it.
	for {
		c.Lock()
		value, ok := c.lru.Get(ref)
		if !ok {
			break
		}
		cr := value.(*cachedRef)
		if !cr.valid {
			// A previous failure. Start clean.
			c.lru.Remove(ref)
			break
		}

		// At this point the ref is cached or in the process of being cached.
		cr.Lock()
		c.Unlock()
		if cr.busy {
			// It is in progress. Wait for the other func to cache it.
			cr.hold.Wait()
			// We loop rather than break because the other func may have failed.
			cr.Unlock()
			continue
		}
		data, err := readFromCacheFile(name)
		cr.Unlock()
		return data, nil, err
	}
	cr := c.newCachedRef(ref)
	c.Unlock()

	// If the store returns different locations, just pass that to the client, don't
	// try to dereference them.
	data, locs, err := store.Get(ref)
	if err != nil || locs != nil {
		cr.Unlock()
		return data, locs, err
	}

	// Cache the data.
	if err := cr.saveToCacheFile(dir, name, data); err != nil {
		cr.Unlock()
		log.Info.Printf("saving cached ref: %s", err)
	}

	// Wake up anyone waiting for us to finish.
	cr.hold.Signal()
	cr.Unlock()
	return data, nil, nil
}

// put saves a reference in the cache.
func (c *storeCache) put(ref upspin.Reference, data []byte) {
	dir, name := c.cacheName(ref)
	c.enforce()

	c.Lock()
	cri, ok := c.lru.Get(ref)
	cr := cri.(*cachedRef)
	if ok {
		// Already cached.
		if cr.valid {
			return
		}
		// Someone else is trying to save it.
		if cr.busy {
			return
		}
		// Last cache attempt failed.
		c.lru.Remove(ref)
	}
	cr = c.newCachedRef(ref)
	c.Unlock()

	// Save the data in a file and remember we cached it.
	if err := cr.saveToCacheFile(dir, name, data); err != nil {
		log.Info.Printf("saving cached ref: %s", err)
	}

	// Wake up anyone waiting for us to finish.
	cr.hold.Signal()
}
