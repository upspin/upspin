// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storecacheserver is a caching proxy between a client and all stores.
package storecacheserver

import (
	"errors"
	"io"
	"os"
	"path"
	"sync"

	"upspin.io/bind"
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

// newCache returns the cache rooted at dir. It will walk the cache to put all files
// into the LRU.
func newCache(dir string, maxBytes int64, maxRefs int) *storeCache {
	c := &storeCache{dir: dir, limit: maxBytes, lru: cache.NewLRU(maxRefs)}
	c.walk(dir)
	return c
}

// walk does a recursive walk of the cache directories adding cached references
// to the LRU.
// TODO(p): We lose ordering doing this. When we add a log for the write
// through cache, we will use it to restore the ordering after this
// operation.
func (c *storeCache) walk(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	info, err := f.Readdir(0)
	f.Close()
	if err != nil {
		return
	}
	if len(info) == 0 {
		// Clean up empty directories.
		os.RemoveAll(dir)
		return
	}
	for _, i := range info {
		pathName := path.Join(dir, i.Name())
		if i.IsDir() {
			c.walk(pathName)
			continue
		}
		cr := c.newCachedRef(pathName)
		cr.size = i.Size()
		cr.valid = true
		cr.busy = false
	}
}

// cachePath builds a path to the local cache file.
// It returns paths to both the containing directory and the file itself.
//
// The actual cache file depends on the server endpoint because we have
// not yet decided on any constraints on reference names, for example
// when mapping host file names to references.
// TODO(p): Revisit when we do.
func (c *storeCache) cachePath(ref upspin.Reference, e upspin.Endpoint) (string, string) {
	dir := path.Join(c.dir, e.String(), string(ref[:2]))
	file := path.Join(dir, string(ref))
	return dir, file
}

// newCachedRef creates a new locked and busy cachedRef.
// Called with c locked.
func (c *storeCache) newCachedRef(file string) *cachedRef {
	cr := &cachedRef{busy: true, c: c}
	cr.hold = sync.NewCond(cr)
	c.lru.Add(file, cr)
	return cr
}

// get fetches a reference.
func (c *storeCache) get(ctx upspin.Context, ref upspin.Reference, store upspin.StoreServer) ([]byte, []upspin.Location, error) {
	dir, file := c.cachePath(ref, store.Endpoint())
	c.enforce()

	// Loop trying to get the reference from the cache. We terminate when we either have
	// gotten the reference or noone else is in the process of getting it.
	var cr *cachedRef
	for {
		c.Lock()
		value, ok := c.lru.Get(file)
		if !ok {
			// First time we've seen this. Create a new cachedRef and add to LRU.
			cr = c.newCachedRef(file)
			cr.Lock()
			c.Unlock()
			break
		}
		cr = value.(*cachedRef)
		cr.Lock()
		c.Unlock()
		if !cr.valid {
			// A previous attempt failed but we left the reference in
			// the LRU.
			break
		}

		// At this point the ref is either cached or in the process of being cached.
		if cr.busy {
			// It is being cached.  Wait till its done.
			cr.hold.Wait()
			// Loop rather than break because the attempt may have failed.
			cr.Unlock()
			continue
		}
		data, err := readFromCacheFile(file)
		if err != nil {
			// The cached copy failed. Try getting it again.
			cr.valid = false
			break
		}
		cr.Unlock()
		return data, nil, nil
	}
	defer func() {
		cr.busy = false
		cr.hold.Signal()
		cr.Unlock()
	}()

	// isError reports whether err is non-nil and remembers it if it is.
	var firstError error
	isError := func(err error) bool {
		if err == nil {
			return false
		}
		if firstError == nil {
			firstError = err
		}
		return true
	}

	// Loop over referred locations.
	var data []byte
	knownLocs := make(map[upspin.Location]bool)
	where := []upspin.Location{upspin.Location{Endpoint: store.Endpoint(), Reference: ref}}
	for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
		loc := where[i]
		store, err := bind.StoreServer(ctx, loc.Endpoint)
		if isError(err) {
			continue
		}
		var locs []upspin.Location
		data, locs, err = store.Get(loc.Reference)
		if isError(err) {
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Success, cache the data.
			if err := cr.saveToCacheFile(dir, file, data); err != nil {
				log.Info.Printf("saving cached ref %s to %s: %s", string(ref), file, err)
			}
			return data, nil, nil
		}
		// Add new locs to the list. Skip ones already there - they've been processed.
		for _, newLoc := range locs {
			if _, found := knownLocs[newLoc]; !found {
				where = append(where, newLoc)
				knownLocs[newLoc] = true
			}
		}
	}

	// Failure.
	return nil, nil, firstError
}

// put saves a reference in the cache.
func (c *storeCache) put(data []byte, store upspin.StoreServer) (upspin.Reference, error) {
	// If we can't put it to the store, don't cache.
	// TODO(p): This will change with a write through cache.
	ref, err := store.Put(data)
	if err != nil {
		return ref, err
	}

	dir, file := c.cachePath(ref, store.Endpoint())
	c.enforce()

	c.Lock()
	value, ok := c.lru.Get(file)
	var cr *cachedRef
	if ok {
		cr = value.(*cachedRef)
		cr.Lock()
		defer cr.Unlock()
		c.Unlock()

		// Already cached or being cached?
		if cr.valid || cr.busy {
			return ref, nil
		}
	} else {
		cr = c.newCachedRef(file)
		cr.Lock()
		defer cr.Unlock()
		c.Unlock()
	}

	// Save the data in a file and remember we cached it.
	if err := cr.saveToCacheFile(dir, file, data); err != nil {
		log.Info.Printf("saving cached ref %s to %s: %s", string(ref), file, err)
	}

	// Wake up anyone waiting for us to finish.
	cr.hold.Signal()
	return ref, nil
}

// delete removes a reference from the cache.
func (c *storeCache) delete(ref upspin.Reference, store upspin.StoreServer) error {
	err := store.Delete(ref)
	if err != nil {
		return err
	}
	_, file := c.cachePath(ref, store.Endpoint())
	value, ok := c.lru.Get(file)
	if !ok {
		return nil
	}
	cr := value.(*cachedRef)
	cr.OnEviction(file)
	return nil
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
func (cr *cachedRef) saveToCacheFile(dir, file string, data []byte) error {
	tmpName := file + ".tmp"
	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		os.MkdirAll(dir, 0700)
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
	if err := os.Rename(tmpName, file); err != nil {
		os.Remove(tmpName)
		return err
	}
	cr.size = int64(len(data))
	cr.valid = true
	cr.busy = false

	// Update the total bytes cached.
	cr.c.inUseLock.Lock()
	cr.c.inUse += cr.size
	cr.c.inUseLock.Unlock()
	return nil
}

// enforce removes the oldest entries until inUse is below limit.
func (c *storeCache) enforce() {
	for {
		c.Lock()
		c.inUseLock.Lock()
		if c.inUse < c.limit {
			c.inUseLock.Unlock()
			c.Unlock()
			break
		}
		key, value := c.lru.RemoveOldest()
		c.inUse -= value.(*cachedRef).size
		c.inUseLock.Unlock()
		c.Unlock()
		os.Remove(key.(string))
	}
}

// OnEviction implements cache.OnEviction.
func (cr *cachedRef) OnEviction(key interface{}) {
	file := key.(string)
	cr.Lock()
	defer cr.Unlock()
	if cr.busy {
		// Someone is trying to read this in. Don't bother removing anything
		// but this is an odd situation so log it.
		log.Info.Printf("cache file busy OnEviction: %s", file)
		return
	}
	cr.valid = false
	c := cr.c
	c.inUseLock.Lock()
	c.inUse -= cr.size
	c.inUseLock.Unlock()
	os.Remove(file)
}
