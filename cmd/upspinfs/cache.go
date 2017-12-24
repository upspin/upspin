// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows
// +build !openbsd

package main // import "upspin.io/cmd/upspinfs"

import (
	"crypto/sha256"
	"fmt"
	"io"
	filepath "path"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"

	lrucache "upspin.io/cache"
	"upspin.io/client"
	"upspin.io/client/clientutil"
	os "upspin.io/cmd/upspinfs/internal/ose"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// Names of cache files are:
//   <cache dir>/<sha256(references)> - for files representing what is in the store.
//   <cachedir>/temp/<number> - for files representing something not yet in the store or
//     a copy in progress from the store.

type cache struct {
	sync.Mutex
	dir    string        // Directory for in-the-clear cached files.
	next   int           // The next sequence to use for temp files.
	client upspin.Client // A client for writing back files.

	// lru is a cache closed but not yet deleted files.
	lru         *lrucache.LRU
	lruBytes    int64 // Sum of storage bytes represented by files in lru.
	lruMaxBytes int64 // Maximum storage bytes allowed for files in lru.
}

const (
	maxRefs = 20
)

type cachedFile struct {
	c       *cache   // cache this belongs to.
	n       *node    // node representing this file.
	fname   string   // Filename in cache.
	inStore bool     // True if this is a cached version of something in the store.
	dirty   bool     // True if it needs to be written back on close.
	seq     int64    // Sequence number of cached file.
	file    *os.File // The cached file.
}

type cachedClosedFile struct {
	c    *cache // cache this file belongs to
	size int64  // size in bytes
}

func newCache(config upspin.Config, dir string, cacheSize int64) *cache {
	c := &cache{dir: dir, client: client.New(config), lru: lrucache.NewLRU(maxRefs), lruMaxBytes: cacheSize}
	os.Mkdir(dir, 0700)

	// Clean out all cache files.
	os.RemoveAll(dir)
	os.MkdirAll(filepath.Join(dir, "tmp"), 0700)

	return c
}

// cacheName builds a path to the local cachefile from all the Locations making up the file.
// It returns paths to both the containing directory and the file itself.
func (c *cache) cacheName(de *upspin.DirEntry) (string, string) {
	x := ""
	for _, b := range de.Blocks {
		x = x + string(b.Location.Reference)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(x)))
	dir := c.dir + "/" + hash[:2]
	file := dir + "/" + hash
	return dir, file
}

// mkTemp returns the name of a new temporary file.
func (c *cache) mkTemp() string {
	c.Lock()
	next := c.next
	c.next++
	c.Unlock()
	return filepath.Join(c.dir, fmt.Sprintf("tmp/%d", next))
}

// create creates a file in the cache.
// The corresponding node should be locked.
func (c *cache) create(h *handle) error {
	const op errors.Op = "cache.create"

	if h.n.cf != nil {
		return errors.E(op, errors.IO, "create of an open file")
	}
	cf := &cachedFile{c: c, dirty: true}
	cf.fname = c.mkTemp()
	var err error
	if cf.file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
		return errors.E(op, err)
	}
	h.n.cf = cf
	cf.n = h.n
	return nil
}

// open opens the cached version of a file.  If it isn't cached, first retrieve it from the store.
// The corresponding node should be locked.
func (c *cache) open(h *handle, flags fuse.OpenFlags) error {
	const op errors.Op = "cache.open"

	n := h.n
	name := n.uname
	if n.cf != nil {
		// We already have a cached version open.
		h.flags = flags
		return nil
	}

	// At this point we may have the reference cached but we first need to look in
	// the directory to see what the reference is.
	dir, err := n.f.dirLookup(n.user)
	if err != nil {
		return errors.E(op, err)
	}
	entry, err := dir.Lookup(name)
	if err != nil {
		// We don't implement links in the standard way. Instead we
		// let FUSE do it by stating every file it walks.
		return errors.E(op, err)
	}

	// If we have a cached version, just return it.
	//
	// We assume that plain pack files are mutable and not completely
	// under our control. They are reread whenever opened.
	cf := &cachedFile{c: c}
	cdir, fname := c.cacheName(entry)
	if entry.Packing != upspin.PlainPack {
		c.Lock()
		// Look for a dirty cached version.
		cf.file, err = os.OpenFile(fname, os.O_RDWR, 0700)
		if err == nil {
			h.flags = flags
			if info, err := cf.file.Stat(); err == nil {
				c.lru.Remove(fname)
				c.Unlock()
				n.cf = cf
				cf.n = n
				n.attr.Size = uint64(info.Size())
				cf.fname = fname
				return nil
			}
		}
		c.Unlock()
	}

	// Invalidate the kernel cache, this is a new version.
	n.f.watched.invalidateChan <- n

	// Create an unpacker to decrypt the file blocks.
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return errors.E(op, name, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(n.f.config, entry)
	if err != nil {
		return errors.E(op, name, err) // Showstopper.
	}

	// Read into a temporary file. We don't want to use it
	// until we've copied over the complete file.
	tmpName := c.mkTemp()
	var file *os.File // The open cache file.
	var offset int64  // The write offset into the cache file.
	if file, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
		return errors.E(op, err)
	}

	for b := 0; ; b++ {
		// Read the next block.
		block, ok := bu.NextBlock()
		if !ok {
			break // EOF
		}
		offset, err = copyBlock(n.f.config, offset, &block, bu, file)
		if err != nil {
			file.Close()
			os.Remove(tmpName)
			return errors.E(op, name, err)
		}
	}

	// Rename to indicate it is in the store.
	if err := os.Rename(tmpName, fname); err != nil {
		os.Mkdir(cdir, 0700)
		if err := os.Rename(tmpName, fname); err != nil {
			file.Close()
			os.Remove(tmpName)
			return errors.E(op, name, err)
		}
	}

	// Set its properties and point the node at it.
	cf.inStore = true
	cf.fname = fname
	cf.file = file
	n.seq = entry.Sequence
	h.flags = flags
	n.attr.Size = uint64(offset)
	n.cf = cf
	cf.n = n
	return nil
}

// CopyBlock reads a block from the store, decrypts it, and writes to the local file.
func copyBlock(cfg upspin.Config, offset int64, block *upspin.DirBlock, bu upspin.BlockUnpacker, file *os.File) (int64, error) {
	if block.Offset != offset {
		return 0, errors.Str("inconsistent block offset")
	}
	cipher, err := clientutil.ReadLocation(cfg, block.Location)
	if err != nil {
		return 0, err
	}
	clear, err := bu.Unpack(cipher)
	if err != nil {
		return 0, err
	}
	n, err := file.WriteAt(clear, offset)
	if err != nil {
		return 0, err
	}
	return offset + int64(n), nil
}

// close is called when the last handle for a node has been closed.
// Called with node locked.
func (cf *cachedFile) close() {
	if cf == nil || cf.file == nil {
		return
	}
	cf.file.Close()
	cf.file = nil
	cf.c.Lock()
	cf.c.lru.Add(cf.fname, &cachedClosedFile{c: cf.c, size: int64(cf.n.attr.Size)})
	if cf.c.lruBytes < 0 {
		log.Error.Print("lruBytes < 0")
		cf.c.lruBytes = 0
	}
	cf.c.lruBytes += int64(cf.n.attr.Size)
	for cf.c.lruBytes > cf.c.lruMaxBytes {
		k, v := cf.c.lru.RemoveOldest()
		if k == nil {
			log.Error.Print("lruBytes > 0 but cache empty")
			cf.c.lruBytes = 0
			break
		}
		v.(*cachedClosedFile).OnEviction(k)
	}
	cf.c.Unlock()
}

// OnEviction is called whenever a file is evicted from the LRU.
// This is called from lru.Add and cf.close with ccf.c locked.
func (ccf *cachedClosedFile) OnEviction(k interface{}) {
	ccf.c.lruBytes -= ccf.size
	if err := os.Remove(k.(string)); err != nil {
		log.Debug.Printf("%s", err)
	}
}

// forget is called when the cached version is no longer correct.
func (cf *cachedFile) forget() {
	if cf == nil {
		return
	}
	cf.close()
	if err := os.Remove(cf.fname); err != nil {
		log.Debug.Printf("%s", err)
	}
}

// clone copies the first size bytes of the old cf.file into a new temp file that replaces it.
func (cf *cachedFile) clone(size int64) error {
	const op errors.Op = "cache.clone"

	fname := cf.c.mkTemp()
	var err error
	file, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		return errors.E(op, err)
	}
	buf := make([]byte, 128*1024)
	for at := int64(0); size < 0 || at < size; {
		rn, rerr := cf.file.ReadAt(buf, at)
		if rn > 0 {
			wn, werr := file.WriteAt(buf[:rn], at)
			if werr != nil {
				file.Close()
				return errors.E(op, werr)
			}
			at += int64(wn)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			file.Close()
			return errors.E(op, rerr)
		}
	}
	cf.file.Close()
	cf.fname = fname
	cf.file = file
	cf.dirty = true
	cf.inStore = false
	return nil
}

// truncate truncates a currently open cached file.  If it represents a reference in the store,
// copy it rather than truncating in place.
func (cf *cachedFile) truncate(n *node, size int64) error {
	const op errors.Op = "cache.truncate"

	// This is the easy case.
	if cf.dirty {
		if err := os.Truncate(cf.fname, size); err != nil {
			return errors.E(op, err)
		}
		return nil
	}

	// This represents an unmodified reference from the store.  Copy it truncating as you go.
	return cf.clone(size)
}

// makeDirty writes the cached file to the store if it is dirty. Called with node locked.
func (cf *cachedFile) markDirty() error {
	// If it's already dirty, nothing to do.
	if cf.dirty {
		return nil
	}
	// Copy on write, sort of.
	return cf.clone(-1)
}

// readAt reads from a cache file.
func (cf *cachedFile) readAt(buf []byte, offset int64) (int, error) {
	return cf.file.ReadAt(buf, offset)
}

// writeAt writes to a cache file.
func (cf *cachedFile) writeAt(buf []byte, offset int64) (int, error) {
	cf.markDirty()
	rv, err := cf.file.WriteAt(buf, offset)
	return rv, err
}

// writeback writes the cached file to the store if it is dirty. Called with node locked.
func (cf *cachedFile) writeback(n *node) error {
	const op errors.Op = "cache.writeback"

	if n.noWB {
		return nil
	}

	// Nothing to do if the cache file isn't dirty.
	if cf == nil {
		return nil
	}
	if !cf.dirty {
		return nil
	}

	// Read the whole file into memory. Hope it fits.
	info, err := cf.file.Stat()
	if err != nil {
		return errors.E(op, err)
	}
	cleartext := make([]byte, info.Size())
	var sofar int64
	for sofar != info.Size() {
		len, err := cf.file.ReadAt(cleartext[sofar:], sofar)
		if len > 0 {
			sofar += int64(len)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.E(op, err)
		}
	}

	// Use the client library to write it back.  Try multiple times on error.
	var de *upspin.DirEntry
	for tries := 0; ; tries++ {
		de, err = cf.c.client.Put(n.uname, cleartext)
		if err == nil {
			n.seq = de.Sequence
			n.attr.Mtime = de.Time.Go()
			break
		}
		if tries > 5 || !strings.Contains(err.Error(), "unreachable") {
			if isXattrFile(n.uname) {
				// A hack so every server doesn't have to handle
				// xattr files. We pretend the Put worked and
				// increase the ref count for the cached version
				// so that the xattr at least lasts as long as
				// upspinfs stays up. Not perfect but keeps
				// macOS finder happy.
				// TODO(p): this might be improved by constraining
				// to fewer error types.
				cf.file.Pin()
				break
			}
			return errors.E(op, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Rename it to reflect the actual reference in the store so that new
	// opens will find the cached version.
	cdir, fname := cf.c.cacheName(de)
	if err := os.Rename(cf.fname, fname); err != nil {
		// Otherwise rename to the common name.
		os.Mkdir(cdir, 0700)
		if err := os.Rename(cf.fname, fname); err != nil {
			return errors.E(op, err)
		}
	}
	cf.fname = fname
	cf.dirty = false
	cf.inStore = true
	return nil
}

// putRedirect assumes that the target fits in a single block.
func (c *cache) putRedirect(n *node, target upspin.PathName) error {
	const op errors.Op = "cache.putRedirect"

	// Use the client library to write it.
	_, err := c.client.PutLink(target, n.uname)
	if err != nil {
		return errors.E(op, err)
	}
	return nil
}

// isXattrFile returns true if the path corresponds to an Xattr file.
func isXattrFile(pathName upspin.PathName) bool {
	p, err := path.Parse(pathName)
	if err != nil {
		return false
	}
	// Base file name must start with "._".
	n := p.NElem()
	return n >= 1 && strings.HasPrefix(p.Elem(n-1), "._")
}
