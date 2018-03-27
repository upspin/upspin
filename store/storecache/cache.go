// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storecache // import "upspin.io/store/storecache"

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/upspin"
)

const (
	logName     = "store.log"
	tmpLogName  = "store.log.tmp"
	flushPeriod = 60 * time.Second
)

// In the following code to avoid deadlock always lock in the order
//   lruLock -> cachedRef
//
// storeCache.Mutex serializes access to the LRU. This prevents two threads simultaneously creating
// the same cachedRef.
//
// cachedRef.Mutex plus cachedRef.hold serialize readers and writers of a cachedRef.

// cachedRef represents a cached object referred to by an upspin.Reference.
type cachedRef struct {
	sync.Mutex
	c      *storeCache
	size   int64
	busy   bool       // True if the ref is in the process of being cached.
	hold   *sync.Cond // Wait here if some other func is caching the ref.
	valid  bool       // True if successfully cached.
	remove bool       // Remove when no longer busy.
}

// storeCache represents a cache for references. If, upon adding to the cache,
// we find more than limit bytes in use, we will remove the oldest entry until below
// the limit. It is possible to push past the limit; it is a soft limit.
//
type storeCache struct {
	inUse int64 // Current bytes cached.
	cfg   upspin.Config

	mu    sync.Mutex
	dir   string     // Top directory for cached references.
	wbDir string     // Top directory for writeback links.
	limit int64      // Soft limit of the maximum bytes to store.
	lru   *cache.LRU // Key is relative path to the cache file. Value is &cachedRef.
	wbq   *writebackQueue
	log   *os.File

	logLock   sync.Mutex
	buffered  *bufio.Writer
	logLen    int64
	oldLogLen int64
}

// newCache returns the cache rooted at dir. It will walk the cache
// to put all files into the LRU and the writeback tree to continue
// trying to write refs back.
func newCache(cfg upspin.Config, dir, wbDir string, maxBytes int64, writethrough bool) (*storeCache, func(upspin.Location), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	maxRefs := int(maxBytes / 128)
	if maxRefs > 10000000 {
		maxRefs = 10000000
	}
	c := &storeCache{cfg: cfg, dir: dir, wbDir: wbDir, limit: maxBytes, lru: cache.NewLRU(maxRefs)}
	var blockFlusher func(upspin.Location)
	if !writethrough {
		c.wbq = newWritebackQueue(c)
		blockFlusher = func(l upspin.Location) { c.wbq.flush(l) }
	}
	c.walk(c.wbDir, "", c.walkedWriteBack)
	c.walk(c.dir, "", c.walkedCachedRef)
	c.readLog()
	c.rewriteLog()
	go c.logFlusher()
	return c, blockFlusher, nil
}

func (c *storeCache) walkedCachedRef(relPath string, size int64) {
	base := filepath.Base(relPath)
	if base == tmpLogName {
		// A previous run died before finishing rewriting
		// the log. Remove the dregs.
		os.Remove(c.absCachePath(relPath))
		return
	}
	if base == logName {
		// Ignore any log file.
		return
	}
	cr := c.newCachedRef(relPath)
	cr.size = size
	cr.valid = false
	cr.busy = false
	atomic.AddInt64(&c.inUse, size)
}

func (c *storeCache) walkedWriteBack(relPath string, size int64) {
	c.wbq.enqueueWritebackFile(relPath)

	// If a matching link doesn't exist in the cache, create one.
	cachePath := c.absCachePath(relPath)
	_, err := os.Stat(cachePath)
	if err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		log.Error.Printf("walkedWriteBack %s: %s", relPath, err)
		return
	}
	wbPath := c.absWritebackPath(relPath)
	if err := os.Link(wbPath, cachePath); err != nil {
		log.Error.Printf("walkedWriteBack %s: %s", relPath, err)
	}
}

func (c *storeCache) absCachePath(relPath string) string {
	return filepath.Join(c.dir, relPath)
}

func (c *storeCache) absWritebackPath(relPath string) string {
	return filepath.Join(c.wbDir, relPath)
}

// walk does a recursive walk of the cache directories adding cached references
// to the LRU. If we encounter errors while walking, try to correct by removing
// the offending files or directories.
// TODO(p): We lose ordering doing this. When we add a log for the write
// through cache, we will use it to restore the ordering after this
// operation.
func (c *storeCache) walk(root, relDirPath string, action func(string, int64)) error {
	absDirPath := filepath.Join(root, relDirPath)
	f, err := os.Open(absDirPath)
	if err != nil {
		return os.RemoveAll(absDirPath)
	}
	info, err := f.Readdir(0)
	f.Close()
	if err != nil {
		log.Error.Printf("walking cache dirs: %s", err)
		return err

	}
	if len(info) == 0 && len(relDirPath) != 0 {
		// Clean up empty directories.
		return os.RemoveAll(absDirPath)
	}
	for _, i := range info {
		relPath := filepath.Join(relDirPath, i.Name())
		if i.IsDir() {
			if err := c.walk(root, relPath, action); err != nil {
				return err
			}
			continue
		}
		action(relPath, i.Size())
	}
	return err
}

// readLog reads the log reordering files. It then creates a shorter version of the log.
func (c *storeCache) readLog() {
	f, err := os.Open(c.absCachePath(logName))
	if err == nil {
		// Order all entries in lru order.
		b := bufio.NewReader(f)
		for {
			// The log is just an in order list of files.
			file, err := b.ReadString('\n')
			if err != nil {
				break
			}
			file = file[:len(file)-1]

			// The Get moves the entry to the front of the LRU.
			value, ok := c.lru.Get(file)
			if ok {
				cr := value.(*cachedRef)
				cr.valid = true
			}
		}
		f.Close()
	}

	// Any files not yet ordered are assumed new, i.e., that part of the log
	// didn't get flushed before exit. Move them to the front of the LRU.
	unordered := []string{}
	i := c.lru.NewIterator()
	for {
		key, value, ok := i.GetAndAdvance()
		if !ok {
			break
		}
		cr := value.(*cachedRef)
		if !cr.valid {
			unordered = append(unordered, key.(string))
			cr.valid = true
		}
	}
	for _, file := range unordered {
		c.lru.Get(file)
	}
}

// rewriteLog writes out a new compressed log.
// Expects to be called with logLock held.
func (c *storeCache) rewriteLog() {
	// Write temporary log.
	tmpLogPath := c.absCachePath(tmpLogName)
	logPath := c.absCachePath(logName)
	f, err := os.Create(tmpLogPath)
	if err != nil {
		log.Error.Printf("creating log file: %s", err)
		return
	}
	buffered := bufio.NewWriter(f)
	logLen := int64(0)
	i := c.lru.NewReverseIterator()
	for {
		key, _, ok := i.GetAndAdvance()
		if !ok {
			break
		}
		if _, err := buffered.WriteString(key.(string) + "\n"); err != nil {
			log.Error.Printf("appending to access log: %s", err)
			break
		}
		logLen++
	}
	if err := buffered.Flush(); err != nil {
		log.Error.Printf("appending to temporary log: %s", err)
	}

	// Rename temporary to permanent log.
	if err := os.Rename(tmpLogPath, logPath); err != nil {
		log.Error.Printf("appending to temporary log: %s", err)
	}

	// Switch to new log.
	c.log.Close()
	c.log, c.buffered, c.logLen, c.oldLogLen = f, buffered, logLen, logLen
}

// logAccess appends the file to the access log.
func (c *storeCache) logAccess(file string) {
	c.logLock.Lock()
	if _, err := c.buffered.WriteString(file + "\n"); err != nil {
		log.Error.Printf("appending to access log: %s", err)
	}
	c.logLen++

	// Trim log length.
	if c.logLen > 10*c.oldLogLen {
		c.rewriteLog()
	}

	c.logLock.Unlock()
}

// logFlusher is a go func that periodically flushes the log.
func (c *storeCache) logFlusher() {
	for {
		time.Sleep(flushPeriod)
		c.logLock.Lock()
		c.buffered.Flush()
		c.logLock.Unlock()
	}
}

// cachePath builds a path to the local cache file.
//
// The actual cache file depends on the server endpoint because we have
// not yet decided on any constraints on reference names, for example
// when mapping host file names to references.
func (c *storeCache) cachePath(ref upspin.Reference, e upspin.Endpoint) string {
	subdir := "zz"
	if len(ref) > 1 {
		subdir = string(ref[:2])
	}
	return filepath.Join(e.String(), subdir, string(ref))
}

// newCachedRef creates a new locked and busy cachedRef.
// Called with c locked.
func (c *storeCache) newCachedRef(file string) *cachedRef {
	cr := &cachedRef{busy: true, c: c}
	cr.hold = sync.NewCond(cr)
	c.lru.Add(file, cr)
	return cr
}

// get fetches a reference. If possible, it stores it as a local file.
// No locks are held on entry or exit.
func (c *storeCache) get(cfg upspin.Config, ref upspin.Reference, e upspin.Endpoint) ([]byte, []upspin.Location, error) {
	if ref == upspin.HealthMetadata {
		return []byte("you never write, you never call, I could be dead for all you know"), nil, nil
	}

	if ref == upspin.FlushWritebacksMetadata {
		// Block until all data is flushed.
		c.wbq.flush(upspin.Location{})
		return []byte("cache flushed"), nil, nil
	}

	file := c.cachePath(ref, e)

	c.enforceByteLimitByRemovingLeastRecentlyUsedFile()

	// The loop terminates either by returning the cached data
	// or while holding the cachedRef's Lock, ready to fetch
	// the data for that reference and populate the cache.
	var cr *cachedRef
	for {
		c.mu.Lock()
		value, ok := c.lru.Get(file)
		if !ok {
			// First time we've seen this. Create a new cachedRef and add to LRU.
			cr = c.newCachedRef(file)
			cr.Lock()
			c.mu.Unlock()
			break
		}
		cr = value.(*cachedRef)
		cr.Lock()
		c.mu.Unlock()
		if !cr.valid {
			// A previous attempt failed but we left the reference in
			// the LRU.
			break
		}

		// At this point the ref is either cached or in the process of being cached.
		if cr.busy {
			// It is being cached.  Wait until it's done.
			cr.hold.Wait()
			// Loop rather than break because the attempt may have failed.
			cr.Unlock()
			continue
		}
		data, err := c.readFromCacheFile(c.absCachePath(file))
		if err != nil {
			// Could not read the cached data.
			// Invalidate the cachedRef so that it will be fetched again.
			cr.valid = false
			break
		}
		cr.Unlock()
		c.logAccess(file)
		return data, nil, nil
	}
	defer func() {
		cr.busy = false
		cr.hold.Signal()
		if cr.remove {
			cr.removeFile(file)
		}
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

	const serviceUnavailable = "503" // String representation of http.StatusServiceUnavailable.

	// If we only see serviceUnavailable errors, retry in the hope we can live through them.
	for tries := 0; tries < 3; tries++ {
		var fatal bool

		// Loop over referred locations.
		var data []byte
		knownLocs := make(map[upspin.Location]bool)
		where := []upspin.Location{upspin.Location{Endpoint: e, Reference: ref}}
		for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
			loc := where[i]
			store, err := bind.StoreServer(cfg, loc.Endpoint)
			if isError(err) {
				continue
			}

			// In case of a serviceUnavailable error, retry a few times.
			var locs []upspin.Location
			var refdata *upspin.Refdata
			data, refdata, locs, err = store.Get(loc.Reference)
			if isError(err) {
				if !strings.Contains(err.Error(), serviceUnavailable) {
					fatal = true
				}
				continue // locs guaranteed to be nil.
			}
			if locs == nil && err == nil {
				// Success, maybe cache the data.
				if !refdata.Volatile {
					if err := cr.saveToCacheFile(file, data); err != nil {
						log.Error.Printf("saving cached ref %s to %s: %s", string(ref), file, err)
					}
				}
				c.logAccess(file)
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
		if fatal {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Failure.
	return nil, nil, firstError
}

// put saves a reference in the cache. put has the same invariants as get.
func (c *storeCache) put(cfg upspin.Config, data []byte, e upspin.Endpoint) (upspin.Reference, error) {
	var ref upspin.Reference
	if c.wbq == nil {
		// If we can't put it to the store, don't cache.
		store, err := bind.StoreServer(cfg, e)
		if err != nil {
			return "", err
		}
		refdata, err := store.Put(data)
		if err != nil {
			return "", err
		}
		ref = refdata.Reference
	} else {
		ref = upspin.Reference(sha256key.Of(data).String())
	}
	file := c.cachePath(ref, e)
	c.enforceByteLimitByRemovingLeastRecentlyUsedFile()

	c.mu.Lock()
	value, ok := c.lru.Get(file)
	var cr *cachedRef
	if ok {
		cr = value.(*cachedRef)
		cr.Lock()
		defer cr.Unlock()
		c.mu.Unlock()

		// Already cached or being cached?
		if cr.valid || cr.busy {
			c.logAccess(file)
			return ref, nil
		}
	} else {
		cr = c.newCachedRef(file)
		cr.Lock()
		defer cr.Unlock()
		c.mu.Unlock()
	}

	// Save the data in a file and remember we cached it.
	if err := cr.saveToCacheFile(file, data); err != nil {
		log.Error.Printf("saving cached ref %s to %s: %s", string(ref), file, err)
		if c.wbq != nil {
			// When writing back, any problem writing the file into the
			// cache is fatal.
			return "", err
		}
	}

	// Add to list of files to write back.
	if c.wbq != nil {
		if err := c.wbq.requestWriteback(ref, e); err != nil {
			return "", err
		}
	}

	// Wake up anyone waiting for us to finish.
	cr.hold.Signal()
	c.logAccess(file)
	return ref, nil
}

// delete removes a reference from the cache.
// - No locks are held on entry or exit.
// - If the cache file is busy, don't remove it.
func (c *storeCache) delete(cfg upspin.Config, ref upspin.Reference, e upspin.Endpoint) error {
	store, err := bind.StoreServer(cfg, e)
	if err != nil {
		return err
	}
	if err := store.Delete(ref); err != nil {
		return err
	}
	file := c.cachePath(ref, e)
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.lru.Get(file)
	if !ok {
		return nil
	}
	cr := value.(*cachedRef)
	cr.Lock()
	defer cr.Unlock()
	if cr.busy {
		return nil
	}
	c.lru.Remove(file)
	cr.removeFile(file)
	return nil
}

// readFromCachefile reads in the cache file, if it exists.
// Called with the cachedFile locked.
func (c *storeCache) readFromCacheFile(file string) ([]byte, error) {
	f, err := os.Open(file)
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
func (cr *cachedRef) saveToCacheFile(file string, data []byte) error {
	tmpName := cr.c.absCachePath(file + ".tmp")
	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		os.MkdirAll(filepath.Dir(tmpName), 0700)
		f, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
		if err != nil {
			return err
		}
	}
	cleanup := func() {
		f.Close()
		if err := os.Remove(tmpName); err != nil {
			log.Info.Printf("removing cache file: %s", err)
		}
	}
	n, err := f.Write(data)
	if err != nil {
		cleanup()
		return err
	}
	if n != len(data) {
		cleanup()
		return errors.New("writing cache file")
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	pathName := cr.c.absCachePath(file)
	if err := os.Rename(tmpName, pathName); err != nil {
		cleanup()
		return err
	}

	cr.size = int64(len(data))
	cr.valid = true
	cr.busy = false

	// If the file was purged from the cache during the put, remove it.
	// Unususual but possible with a small cache and simultaneous puts.
	if cr.remove {
		cr.removeFile(file)
	}

	// Update the total bytes cached.
	atomic.AddInt64(&cr.c.inUse, cr.size)
	return nil
}

// enforceByteLimitByRemovingLeastRecentlyUsedFile removes the oldest entries until inUse is below limit. We take a leap
// of faith that the least recently used entry is not currently in use.
func (c *storeCache) enforceByteLimitByRemovingLeastRecentlyUsedFile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if atomic.LoadInt64(&c.inUse) < c.limit {
			break
		}
		key, value := c.lru.RemoveOldest()
		if value == nil {
			// Nothing left.
			log.Info.Printf("exceeding cache byte limit")
			break
		}
		value.(*cachedRef).OnEviction(key)
	}
}

// OnEviction implements cache.OnEviction.
func (cr *cachedRef) OnEviction(key interface{}) {
	file := key.(string)
	cr.Lock()
	defer cr.Unlock()
	if cr.busy {
		// Someone is trying to read this in or put it. Don't bother removing anything
		// but this is an odd situation so log it.
		log.Info.Printf("cache file busy on eviction: %s", file)
		// Remember to remove it when it is no longer busy.
		cr.remove = true
		return
	}
	cr.removeFile(file)
}

// removeFile removes a file from the cache and updates the count of bytes in use.
// This is called with cr locked.
func (cr *cachedRef) removeFile(file string) {
	cr.valid = false
	cr.remove = false
	atomic.AddInt64(&cr.c.inUse, -cr.size)
	if err := os.Remove(cr.c.absCachePath(file)); err != nil {
		log.Info.Printf("can't remove file on eviction: %s", err)
	}
}
