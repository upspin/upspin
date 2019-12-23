// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

package main // import "upspin.io/cmd/upspinfs"

// Open files and a small cache of previously opened ones are cached
// locally in disk files. File blocks are downloaded on demand when
// read. If an existing file is written to, the whole file is read in
// since a new encryption key is chosen on writeback and old blocks
// will no longer be valid even if unchanged.
//
// The local disk cache files are encrypted using a key chosen at
// startup. Therefore all old cache files are removed at startup.
//
// TODO(p): Think about changing the encryption key behavior to avoid
// rewriting unchanged blocks? It has security implications.

import (
	"crypto/sha256"
	"fmt"
	"io"
	filepath "path"
	"strings"
	"sync"
	"sync/atomic"
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
	maxRefs         = 100  // Maximum closed files we will cache.
	cachedFilePerms = 0600 // Permissions for cached files.
	cacheDirPerms   = 0700 // Permissions for intermediate directories in cache tree.
)

type cachedFile struct {
	c       *cache   // cache this belongs to.
	n       *node    // node representing this file.
	fname   string   // Filename in cache.
	inStore bool     // True if this is a cached version of something in the store.
	dirty   bool     // True if it needs to be written back on close.
	file    *os.File // The cached file.
	size    int64    // size of file in bytes.

	// The following are used when demand loading existing files to keep
	// track of what blocks have been loaded and an unpacker to do the
	// decryption.
	de            *upspin.DirEntry
	blocksLoaded  []bool // True if the corresponding block has been downloaded.
	nBlocksLoaded int    // Number of blocks downloaded.
	bu            upspin.BlockUnpacker
	cachedSize    int64
}

// Used only in testing. Incremented whenever a cacheblock is downloaded to a local cachefile.
var cacheBlocksLoaded int64

func newCache(config upspin.Config, dir string, cacheSize int64) *cache {
	c := &cache{dir: dir, client: client.New(config), lru: lrucache.NewLRU(maxRefs), lruMaxBytes: cacheSize}
	os.Mkdir(dir, cacheDirPerms)

	// Clean out all cache files.
	os.RemoveAll(dir)
	os.MkdirAll(filepath.Join(dir, "tmp"), cacheDirPerms)

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

// create creates a file in the cache. This file will not be added to the cache LRU
// until close & writeback.
// The corresponding node should be locked.
func (c *cache) create(h *handle) error {
	const op errors.Op = "cache.create"

	if h.n.cf != nil {
		return errors.E(op, errors.IO, "create of an open file")
	}
	cf := &cachedFile{c: c, dirty: true}
	cf.fname = c.mkTemp()
	var err error
	if cf.file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, cachedFilePerms); err != nil {
		return errors.E(op, err)
	}
	h.n.cf = cf
	cf.n = h.n

	// create is only called in response to a Create request from FUSE. FUSE
	// only sends Create requests when it believes the file doesn't exist so
	// set the sequence for this file to SeqNotExist thus conditioning writeback
	// succeeding on none else having created the file in the mean time.
	h.n.seq = upspin.SeqNotExist

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

	// Look for an already cached version.
	c.Lock()
	value, ok := c.lru.Get(name)
	var cf *cachedFile
	if ok {
		// Remove from the cache of closed files since it is now open
		// or about to be removed.
		cf = value.(*cachedFile)
		c.lru.Remove(name)
		c.lruBytes -= cf.cachedSize
		c.Unlock()

		// Is it the right version and can we open it? cf.de is nil if the cached file
		// never got committed to the DirServer.
		if cf.de != nil && cf.de.Sequence == entry.Sequence {
			cf.file, err = os.OpenFile(cf.fname, os.O_RDWR, cachedFilePerms)
			if err == nil {
				h.flags = flags
				n.cf = cf
				cf.n = n
				n.attr.Size = uint64(cf.size)
				h.n.seq = entry.Sequence
				return nil
			}
		}

		// Either the cached version couldn't be opened or had the wrong sequence.
		os.Remove(cf.fname)
	} else {
		c.Unlock()
	}

	// No cached version found, create a new one.
	dname, fname := c.cacheName(entry)
	cf = &cachedFile{
		c:     c,
		n:     n,
		fname: fname,
	}
	h.n.seq = entry.Sequence

	// Invalidate the kernel cache, this is a new version.
	n.f.watched.invalidateChan <- n

	if err := cf.attachDirEntry(n.f.config, entry, false); err != nil {
		return errors.E(op, err)
	}

	// Create the new cache file.
	os.Mkdir(dname, cacheDirPerms)
	if cf.file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, cachedFilePerms); err != nil {
		return errors.E(op, err)
	}

	// Get the first block to make sure we can. If we didn't
	// do this we would have to check accessability and that is
	// complicated.
	if cf.size > 0 {
		if err := cf.download(0, 1); err != nil {
			cf.file.Close()
			os.Remove(cf.fname)
			return errors.E(op, name, err)
		}
	}

	// Set its properties and point the node at it.
	cf.inStore = true
	cf.fname = fname
	n.seq = entry.Sequence
	h.flags = flags
	n.attr.Size = uint64(cf.size)
	n.cf = cf
	return nil
}

// attachDirEntry links a cachedFile and a DirEntry. If the file has not been downloaded,
// it also creates a block unpacker to be used when demand loading.
func (cf *cachedFile) attachDirEntry(config upspin.Config, de *upspin.DirEntry, downloaded bool) error {
	var err error
	if cf.size, err = de.Size(); err != nil {
		return err
	}
	cf.de = de
	cf.blocksLoaded = make([]bool, len(de.Blocks))
	if !downloaded {
		// Create an unpacker to decrypt the file blocks.
		packer := pack.Lookup(de.Packing)
		if packer == nil {
			return errors.E(de.Name, errors.Errorf("unrecognized Packing %d", de.Packing))
		}
		bu, err := packer.Unpack(config, de)
		if err != nil {
			return errors.E(de.Name, err)
		}
		cf.bu = bu
	} else {
		cf.nBlocksLoaded = len(de.Blocks)
		for i := range cf.blocksLoaded {
			cf.blocksLoaded[i] = true
		}
	}
	return nil
}

// detachDirEntry unlinks a DirEntry and a cachedFile.
func (cf *cachedFile) detachDirEntry() {
	cf.de = nil
	cf.blocksLoaded = nil
	cf.nBlocksLoaded = 0
	cf.bu = nil
}

// download insures that the local cache file contains at least the region specified
// by the offset and size parameters.
func (cf *cachedFile) download(offset int64, size int64) error {
	if offset < 0 {
		return errors.Errorf("downloading %s: bad offset %d", cf.de.Name, offset)
	}
	if size < 0 {
		return errors.Errorf("downloading %s: bad size %d", cf.de.Name, size)
	}
	if cf.de == nil || cf.nBlocksLoaded >= len(cf.de.Blocks) {
		return nil // nothing to download
	}
	// Find beginning block in sequence.
	bi := 0
	for ; bi < len(cf.de.Blocks); bi++ {
		b := &cf.de.Blocks[bi]
		if offset >= b.Offset && offset < b.Offset+b.Size {
			break
		}
	}
	if bi >= len(cf.de.Blocks) {
		return nil
	}

	// Download blocks covering the range.
	end := offset + size
	for {
		// Read the next block.
		block, ok := cf.bu.SeekBlock(bi)
		if !ok {
			return nil // EOF
		}
		if block.Offset >= end {
			return nil
		}
		if !cf.blocksLoaded[bi] {
			// Not yet downloaded, download and decrypt.
			cipher, err := clientutil.ReadLocation(cf.n.f.config, block.Location)
			if err != nil {
				return err
			}
			clear, err := cf.bu.Unpack(cipher)
			if err != nil {
				return err
			}
			for sofar := 0; sofar < len(clear); {
				n, err := cf.file.WriteAt(clear[sofar:], block.Offset+int64(sofar))
				if err != nil {
					return err
				}
				sofar += n
			}
			cf.nBlocksLoaded++
			atomic.AddInt64(&cacheBlocksLoaded, 1)
			cf.blocksLoaded[bi] = true
		}
		bi++
	}
	return nil
}

// close is called when the last handle for a node has been closed.
// Called with node locked.
func (cf *cachedFile) close() {
	if cf == nil || cf.file == nil {
		return
	}
	// Use the size of bytes cached rather than of the whole file.
	info, err := cf.file.Stat()
	if err != nil {
		// if we can't stat the file (for example, if the user removed it), don't bother remembering it.
		cf.file.Close()
		os.Remove(cf.fname)
		return
	}
	cf.cachedSize = info.Size()
	cf.file.Close()
	cf.file = nil
	uname := cf.n.uname
	cf.n = nil
	cf.c.Lock()
	defer cf.c.Unlock()
	cf.c.lru.Add(uname, cf)
	if cf.c.lruBytes < 0 {
		log.Error.Print("lruBytes < 0")
		cf.c.lruBytes = 0
	}

	cf.c.lruBytes += cf.cachedSize
	for cf.c.lruBytes > cf.c.lruMaxBytes {
		k, v := cf.c.lru.RemoveOldest()
		if k == nil {
			log.Error.Print("lruBytes > 0 but cache empty")
			cf.c.lruBytes = 0
			break
		}
		v.(*cachedFile).OnEviction(k)
	}
}

// OnEviction is called whenever a file is evicted from the LRU.
// This is called from lru.Add and cf.close, both with cf.c locked.
func (cf *cachedFile) OnEviction(k interface{}) {
	cf.c.lruBytes -= cf.cachedSize
	if err := os.Remove(cf.fname); err != nil {
		log.Debug.Printf("%s", err)
	}
}

// clone copies the first size bytes of the old cf.file into a new temp file that replaces it.
func (cf *cachedFile) clone(size int64) error {
	const op errors.Op = "cache.clone"
	if size < 0 {
		size = 1 << 62
	}
	if err := cf.download(0, size); err != nil {
		return errors.E(op, err)
	}

	fname := cf.c.mkTemp()
	var err error
	file, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, cachedFilePerms)
	if err != nil {
		return errors.E(op, err)
	}
	buf := make([]byte, 128*1024)
	for at := int64(0); at < size; {
		bsize := size - at
		if bsize > int64(len(buf)) {
			bsize = int64(len(buf))
		}
		rn, rerr := cf.file.ReadAt(buf[:bsize], at)
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
	cf.detachDirEntry()
	cf.file.Close()
	cf.fname = fname
	cf.file = file
	cf.dirty = true
	cf.inStore = false
	return nil
}

// truncate truncates or extends with zeros a currently open cached file.
// If it represents a reference in the store, copy it rather than truncating in place.
func (cf *cachedFile) truncate(n *node, size int64) error {
	const op errors.Op = "cache.truncate"
	usize := uint64(size)
	if usize == n.attr.Size || size < 0 {
		return nil
	}

	if cf.dirty {
		// This is already a temporary file, just change it.
		if usize < n.attr.Size {
			if err := os.Truncate(cf.fname, size); err != nil {
				return errors.E(op, err)
			}
		}
	} else {
		// This represents an unmodified reference from the store.
		// Copy it truncating as you go.
		if err := cf.clone(size); err != nil {
			return errors.E(op, err)
		}
	}

	// If this was a true truncation, we're done.
	if size < int64(n.attr.Size) {
		n.attr.Size = usize
		return nil
	}

	// Extend with zeros. At this point, we're guaranteed that this is a dirty file.
	zeros := make([]byte, 4096)
	buf := make([]byte, 4096)
	for usize > n.attr.Size {
		// Reinitialize the buf every round since writeAt changes it.
		copy(buf, zeros)
		toWrite := usize - n.attr.Size
		if toWrite > uint64(len(buf)) {
			toWrite = uint64(len(buf))
		}
		m, err := cf.writeAt(buf[:toWrite], int64(n.attr.Size))
		n.attr.Size += uint64(m)
		if err != nil {
			return errors.E(op, err)
		}
	}
	cf.size = size
	return nil
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
	if err := cf.download(offset, int64(len(buf))); err != nil {
		return 0, err
	}
	return cf.file.ReadAt(buf, offset)
}

// writeAt writes to a cache file.
func (cf *cachedFile) writeAt(buf []byte, offset int64) (int, error) {
	cf.markDirty()
	rv, err := cf.file.WriteAt(buf, offset)
	if err == nil {
		end := offset + int64(rv)
		if end > cf.size {
			cf.size = end
		}
	}
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
		de, err = cf.c.client.PutSequenced(n.uname, n.seq, cleartext)
		if err == nil {
			n.seq = de.Sequence
			cf.attachDirEntry(n.f.config, de, true)
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
		os.Mkdir(cdir, cacheDirPerms)
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
