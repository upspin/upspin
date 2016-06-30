// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha1"
	"fmt"
	"os"
	filepath "path"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
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

	file *os.File           // The in the clear cached file.
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

// mkTemp returns the name of a new emporary file.
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
	n := h.n
	if n.cf != nil {
		// We already have a cached version open.
		h.flags = flags
		return nil
	}

	// At this point we may have the reference cached but we first need to look in
	// the directory to see what the reference is.
	cf := &cachedFile{c: c, inStore: true}
	dir, err := n.f.dc.lookup(n.user)
	if err != nil {
		return err
	}
	de, err := dir.Lookup(n.uname)
	if err != nil {
		return err
	}

	// Loop following redirects from the store.
	var finalErr error
	locations := []upspin.Location{de.Location}
	for i := 0; i < len(locations); i++ {
		loc := locations[i]
		store, err := bind.Store(n.f.context, loc.Endpoint)
		if err != nil {
			finalErr = err
			continue
		}
		var cdir string
		cdir, cf.fname = c.cacheName(loc, n.uname)

		// We assume that plain pack files are mutable and not conpletely
		// under our control.  Only encrypted files are immutable and can
		// be reused.
		if de.Metadata.Packing() != upspin.PlainPack {
			// Look for a dirty cached version.
			cf.file, err = os.OpenFile(cf.fname, os.O_RDWR, 0700)
			if err == nil {
				h.flags = flags
				if info, err := cf.file.Stat(); err == nil {
					n.cf = cf
					n.attr.Size = uint64(info.Size())
					return nil
				}
			}
		}
		var data []byte
		var locs []upspin.Location
		if data, locs, err = store.Get(loc.Reference); err != nil {
			finalErr = err
			continue
		}
		if len(locs) > 0 {
			log.Debug.Printf("%v redirects to %v", loc, locs)
		outer:
			for _, newLoc := range locs {
				for _, oldLoc := range locations {
					if oldLoc == newLoc {
						continue outer
					}
				}
				locations = append(locations, newLoc)
			}
			continue
		}
		packer := pack.Lookup(de.Metadata.Packing())
		if packer == nil {
			finalErr = errors.E(errors.IO, errors.Str("no packer found"))
			continue
		}
		clearLen := packer.UnpackLen(n.f.context, data, de)
		if clearLen < 0 {
			finalErr = errors.E(errors.IO, errors.Str("unpack len < 0"))
			continue
		}
		cleartext := make([]byte, clearLen)
		rlen, err := packer.Unpack(n.f.context, cleartext, data, de)
		if err != nil {
			finalErr = err
			continue
		}
		cleartext = cleartext[:rlen]
		// Save a copy of the cleartext in the local file system.
		var file *os.File
		if file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
			os.Mkdir(cdir, 0777)
			if file, err = os.OpenFile(cf.fname, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700); err != nil {
				return err
			}
		}
		if wlen, err := file.Write(cleartext); err != nil || rlen != wlen {
			file.Close()
			return err
		}
		cf.file = file
		h.flags = flags
		n.attr.Size = uint64(rlen)
		n.cf = cf
		return nil
	}
	return finalErr
}

// close is called when the last handle for a node has been closed.
// Called with node locked.
func (cf *cachedFile) close() {
	if cf != nil && cf.file != nil {
		cf.file.Close()
	}
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

	// Need to rename it since it no longer represents what's in the store.
	fname := cf.c.mkTemp()
	err := os.Rename(cf.fname, fname)
	if err != nil {
		return err
	}
	cf.fname = fname
	return nil
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
	var loc upspin.Location
	for tries := 0; ; tries++ {
		log.Debug.Printf("Put %q", n)
		loc, err = cf.c.client.Put(n.uname, cleartext)
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
	// opens will find the cached version.
	cdir, fname := cf.c.cacheName(loc, n.uname)
	if err := os.Rename(cf.fname, fname); err != nil {
		os.Mkdir(cdir, 0700)
		if err := os.Rename(cf.fname, fname); err != nil {
			return err
		}
	}
	cf.fname = fname
	return nil
}

func (c *cache) cacheName(loc upspin.Location, uname upspin.PathName) (string, string) {
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(string(loc.Reference)+"!"+string(uname))))
	dir := c.dir + "/" + hash[:2]
	file := dir + "/" + hash
	return dir, file
}

func (c *cache) putRedirect(n *node, target string) error {
	// Use the client library to write it.
	loc, err := c.client.Put(n.uname, []byte(target))
	if err != nil {
		return err
	}

	// Save it in the cache. If we can't, that's fine.
	cdir, fname := c.cacheName(loc, n.uname)
	file, err := os.Create(fname)
	if err != nil {
		os.Mkdir(cdir, 0700)
		file, err = os.Create(fname)
		if err != nil {
			return nil
		}
	}
	if _, err := file.Write([]byte(target)); err != nil {
		os.Remove(fname)
	}
	file.Close()
	return nil
}
