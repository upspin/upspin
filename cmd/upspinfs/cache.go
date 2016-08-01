// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	filepath "path"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
	os "upspin.io/cmd/upspinfs/internal/ose"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/upspin"
)

// Names of cache files are:
//   <cache dir>/<sha1(reference)> - for files representing what is in the store.
//   <cachedir>/temp/<number> - for files representing something not yet in the store or
//     a copy in progress from the store.

type cache struct {
	sync.Mutex
	dir    string        // Directory for in-the-clear cached files.
	next   int           // The next sequence to use for temp files.
	client upspin.Client // A client for writing back files.
}

type cachedFile struct {
	c       *cache // cache this belongs to.
	fname   string // Filename in cache.
	inStore bool   // True if this is a cached version of something in the store.
	dirty   bool   // True if it needs to be written back on close.

	file *os.File           // The cached file.
	de   []*upspin.DirEntry // If this is a directory, its contents.
}

func newCache(context upspin.Context, dir string) *cache {
	c := &cache{dir: dir, client: client.New(context)}
	os.Mkdir(dir, 0700)

	// Clean out any temporary files.
	temp := filepath.Join(dir, "temp")
	os.RemoveAll(temp)
	os.Mkdir(temp, 0700)

	return c
}

// cacheName buids a path to the local cachefile from all the Locations making up the file.
// It returns paths to both the containing directory and the file itself.
func (c *cache) cacheName(de *upspin.DirEntry) (string, string) {
	x := ""
	for _, b := range de.Blocks {
		x = x + string(b.Location.Reference)
	}
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(x)))
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
	return filepath.Join(c.dir, fmt.Sprintf("temp/%d", next))
}

// create creates a file in the cache.
// The corresponding node should be locked.
func (c *cache) create(h *handle) error {
	if h.n.cf != nil {
		return errors.E(errors.IO, errors.Str("create of an open file"))
	}
	cf := &cachedFile{c: c, dirty: true}
	cf.fname = c.mkTemp()
	var err error
	if cf.file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
		return err
	}
	h.n.cf = cf
	return nil
}

// open opens the cached version of a file.  If it isn't cached, first retrieve it from the store.
// The corresponding node should be locked.
func (c *cache) open(h *handle, flags fuse.OpenFlags) error {
	op := "open"
	n := h.n
	name := n.uname
	if n.cf != nil {
		// We already have a cached version open.
		h.flags = flags
		return nil
	}

	// At this point we may have the reference cached but we first need to look in
	// the directory to see what the reference is.
	dir := n.f.dirLookup(n.user)
	entry, err := dir.Lookup(name)
	if err != nil {
		return err
	}

	// firstError remembers the first error we saw. If we fail completely we return it.
	var firstError error
	// isError reports whether err is non-nil and remembers it if it is.
	isError := func(err error) bool {
		if err == nil {
			return false
		}
		if firstError == nil {
			firstError = err
		}
		return true
	}

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return errors.E(op, name, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(n.f.context, entry)
	if err != nil {
		return errors.E(op, name, err) // Showstopper.
	}

	// If we have a cached version, just return it.
	//
	// We assume that plain pack files are mutable and not conpletely
	// under our control.  Only encrypted files are immutable and can
	// be reused.
	cf := &cachedFile{c: c, inStore: true}
	cdir, fname := c.cacheName(entry)
	if entry.Packing != upspin.PlainPack {
		// Look for a dirty cached version.
		cf.file, err = os.OpenFile(fname, os.O_RDWR, 0700)
		if err == nil {
			h.flags = flags
			if info, err := cf.file.Stat(); err == nil {
				n.cf = cf
				n.attr.Size = uint64(info.Size())
				return nil
			}
		}
		cf.fname = fname
	}

	// No cached version.  Fetch and cache.
	var file *os.File // The open cache file.
	var at int64      // The write offset into the cache file.
	if file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
		os.Mkdir(cdir, 0777)
		if file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
			return err
		}
	}
Blocks:
	for b := 0; ; b++ {
		block, ok := bu.NextBlock()
		if !ok {
			break // EOF
		}
		// Get the data for this block.
		// where is the list of locations to examine. It is updated in the loop.
		where := []upspin.Location{block.Location}
		for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
			loc := where[i]
			store, err := bind.StoreServer(n.f.context, loc.Endpoint)
			if isError(err) {
				continue
			}
			cipher, locs, err := store.Get(loc.Reference)
			if isError(err) {
				continue // locs guaranteed to be nil.
			}
			if locs == nil && err == nil {
				// Found the data. Unpack it.
				clear, err := bu.Unpack(cipher)
				if err != nil {
					file.Close()
					os.Remove(cf.fname)
					return errors.E(op, name, err) // Showstopper.
				}
				// Write it to the local cache file.
				n, err := file.WriteAt(clear, at)
				if err != nil {
					file.Close()
					os.Remove(cf.fname)
					return errors.E(op, name, err) // Showstopper.
				}
				at += int64(n)
				continue Blocks
			}
			// Add new locs to the list. Skip ones already there - they've been processed. TODO: n^2.
		outer:
			for _, newLoc := range locs {
				for _, oldLoc := range where {
					if oldLoc == newLoc {
						continue outer
					}
				}
				where = append(where, newLoc)
			}
		}
		// If we arrive here, we have failed to find a block.
		// TODO: custom error types.
		if firstError != nil {
			file.Close()
			os.Remove(cf.fname)
			return errors.E(op, name, firstError)
		}
		return errors.Errorf("client: data for block %d in %q not found on any store server", b, name)
	}

	// Remember cached file.
	cf.file = file
	h.flags = flags
	n.attr.Size = uint64(at)
	n.cf = cf
	return nil
}

// close is called when the last handle for a node has been closed.
// Called with node locked.
func (cf *cachedFile) close() {
	if cf == nil || cf.file == nil {
		return
	}
	cf.file.Close()
}

// clone copies the first size bytes of the old cf.file into a new temp file that replaces it.
func (cf *cachedFile) clone(size int64) error {
	fname := cf.c.mkTemp()
	var err error
	file, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}
	buf := make([]byte, 128*1024)
	for at := int64(0); size < 0 || at < size; {
		_, err := cf.file.ReadAt(buf, at)
		if err != nil {
			if err == io.EOF {
				break
			}
			file.Close()
			return err
		}
		wn, err := file.WriteAt(buf, at)
		if err != nil {
			file.Close()
			return err
		}
		at += int64(wn)
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
	// This is the easy case.
	if !cf.inStore {
		if err := os.Truncate(cf.fname, size); err != nil {
			return err
		}
		cf.dirty = true
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
	cf.dirty = true

	// If it isn't in the store, marking it is enough.
	if !cf.inStore {
		return nil
	}

	// Need to copy it since it no longer represents what's in the store.
	return cf.clone(0)
}

// readAt reads from a cache file.
func (cf *cachedFile) readAt(buf []byte, offset int64) (int, error) {
	return cf.file.ReadAt(buf, offset)
}

// writeAt writes to a cache file.
func (cf *cachedFile) writeAt(buf []byte, offset int64) (int, error) {
	cf.markDirty()
	return cf.file.WriteAt(buf, offset)
}

// writeBack writes the cached file to the store if it is dirty. Called with node locked.
func (cf *cachedFile) writeBack(h *handle) error {
	n := h.n

	// Nothing to do if the cache file isn't dirty.
	if !cf.dirty {
		return nil
	}

	// Read the whole file into memory. Hope it fits.
	log.Debug.Printf("writeBack %q, %s opened", n, cf.fname)
	info, err := cf.file.Stat()
	if err != nil {
		return err
	}
	cleartext := make([]byte, info.Size())
	var sofar int64
	for sofar != info.Size() {
		len, err := cf.file.ReadAt(cleartext[sofar:], sofar)
		if err != nil {
			return err
		}
		sofar += int64(len)
	}

	// Hack because zero length access files don't work.
	// TODO(p): fix when 0 length access files are allowed.
	if len(cleartext) == 0 && access.IsAccessFile(n.uname) {
		cleartext = []byte("\n")
	}
	log.Debug.Printf("writeBack %q, %s read", n, cf.fname)

	// Use the client library to write it back.  Try multiple times on error.
	var de *upspin.DirEntry
	for tries := 0; ; tries++ {
		log.Debug.Printf("Put %q", n)
		de, err = cf.c.client.Put(n.uname, cleartext)
		if err == nil {
			break
		}
		if tries > 5 || !strings.Contains(err.Error(), "unreachable") {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug.Printf("done Put %q", n)
	cf.dirty = false

	// Rename it to reflect the actual reference in the store so that new
	// opens will find the cached version.  Assume a single block.
	// TODO(p): what if it isn't a single block?
	cdir, fname := cf.c.cacheName(de)
	if err := os.Rename(cf.fname, fname); err != nil {
		os.Mkdir(cdir, 0700)
		if err := os.Rename(cf.fname, fname); err != nil {
			return err
		}
	}
	cf.fname = fname
	return nil
}

// putRedirect assumes that the target fits in a single block.
func (c *cache) putRedirect(n *node, target string) error {
	// Use the client library to write it.
	de, err := c.client.Put(n.uname, []byte(target))
	if err != nil {
		return err
	}

	// Save it in the cache. If we can't, that's fine.
	cdir, fname := c.cacheName(de)
	file, err := os.Create(fname)
	if err != nil {
		os.Mkdir(cdir, 0700)
		file, err = os.Create(fname)
		if err != nil {
			return nil
		}
	}
	if _, err := file.WriteAt([]byte(target), 0); err != nil {
		os.Remove(fname)
	}
	file.Close()
	return nil
}
