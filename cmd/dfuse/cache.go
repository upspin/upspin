package main

import (
	"crypto/sha1"
	"fmt"
	"log"
	"os"
	filepath "path"
	"sync"

	"bazil.org/fuse"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
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
}

func newCache(context *upspin.Context, dir string) *cache {
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
		return eio("unexpected create of an open file")
	}
	cf := &cachedFile{c: c, dirty: true}
	cf.fname = c.mkTemp()
	var err error
	if h.file, err = os.Create(cf.fname); err != nil {
		return eio("creating %q file %q: %s", h.n.uname, cf.fname, err)
	}
	h.n.cf = cf
	return nil
}

// open opens the cached version of a file.  If it isn't cached, first retrieve it from the store.
// The corresponding node should be locked.
func (c *cache) open(h *handle, flags fuse.OpenFlags) error {
	n := h.n
	if n.cf != nil {
		// Use precached version.
		var err error
		h.file, err = os.OpenFile(n.cf.fname, int(flags), 0700)
		if err != nil {
			return eio("opening %q file %q: %s", h.n.uname, n.cf.fname, err)
		}
		return nil
	}

	// At this point we may have the reference cached but we first need to look in
	// the directory to see what the reference is.
	cf := &cachedFile{c: c, inStore: true}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return enoent("%q", n.user)
	}
	de, err := ue.dir.Lookup(n.uname)
	if err != nil {
		return enoent("%q", n.uname)
	}

	// Loop following redirects from the store.
	var finalErr error
	locations := []upspin.Location{de.Location}
	for i := 0; i < len(locations); i++ {
		loc := locations[i]
		store := n.f.context.Store
		if loc.Endpoint != store.Endpoint() {
			var err error
			store, err = bind.Store(n.f.context, loc.Endpoint)
			if err != nil {
				finalErr = eio("%s bind.Store %v", err, loc)
				continue
			}
		}
		cf.fname = filepath.Join(c.dir, fingerprint(loc))

		// We assume that plain pack files are mutable and not conpletely
		// under our control.  Only encrypted files are immutable and can
		// be reused.
		if de.Metadata.Packing() != upspin.PlainPack {
			// Look for a dirty cached version.
			h.file, err = os.OpenFile(cf.fname, int(flags), 0700)
			if err == nil {
				h.flags = flags
				if info, err := h.file.Stat(); err == nil {
					n.cf = cf
					n.attr.Size = uint64(info.Size())
					return nil
				}
			}
		}
		var data []byte
		var locs []upspin.Location
		if data, locs, err = store.Get(loc.Reference); err != nil {
			finalErr = eio("%s Get %q ref %q file %q", err, n.uname, loc.Reference, cf.fname)
			continue
		}
		if len(locs) > 0 {
			log.Printf("%v redirects to %v", loc, locs)
		outer:
			for _, newLoc := range locs {
				for _, oldLoc := range locations {
					if oldLoc == newLoc {
						continue outer
					}
				}
				locations = append(locations, newLoc)
			}
			locations = append(locations, locs...)
			continue
		}
		packer := pack.Lookup(de.Metadata.Packing())
		if packer == nil {
			finalErr = eio("couldn't lookup %q ref %q file %q", n.uname, loc.Reference, cf.fname)
			continue
		}
		clearLen := packer.UnpackLen(n.f.context, data, &de.Metadata)
		if clearLen < 0 {
			finalErr = eio("couldn't unpack %q ref %q file %q", h.n.uname, loc.Reference, cf.fname)
			continue
		}
		cleartext := make([]byte, clearLen)
		rlen, err := packer.Unpack(n.f.context, cleartext, data, &de.Metadata, h.n.uname)
		if err != nil {
			finalErr = eio("%s unpacking %q ref %q file %q", err, h.n.uname, loc.Reference, cf.fname)
			continue
		}
		cleartext = cleartext[:rlen]
		// Save a copy of the cleartext in the local file system.
		var file *os.File
		if file, err = os.Create(cf.fname); err != nil {
			return eio("%s creating %q ref %q file %q", err, h.n.uname, loc.Reference, cf.fname)
		}
		if wlen, err := file.Write(cleartext); err != nil || rlen != wlen {
			file.Close()
			return eio("%s writing %q ref %q file %q", err, h.n.uname, loc.Reference, cf.fname)
		}
		if h.file, err = os.OpenFile(cf.fname, int(flags), 0700); err != nil {
			return eio("%s opening %q ref %q file %q", err, n.uname, loc.Reference, cf.fname)
		}
		h.flags = flags
		n.attr.Size = uint64(rlen)
		n.cf = cf
		return nil
	}
	return finalErr
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
		return eio("renaming %q to %q: %s", cf.fname, fname, err)
	}
	cf.fname = fname
	return nil
}

// writeBack writes the cached file to the store if it is dirty. Called with node locked.
func (cf *cachedFile) writeBack(n *node) error {
	// Nothing to do if the cache file isn't dirty.
	if !cf.dirty {
		return nil
	}

	// Read the whole file into memory. Hope it fits.
	file, err := os.Open(cf.fname)
	if err != nil {
		return eio("opening %q (%q): %s", cf.fname, n.uname, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return eio("stating %q (%q): %s", cf.fname, n.uname, err)
	}
	cleartext := make([]byte, info.Size())
	var sofar int64
	for sofar != info.Size() {
		len, err := file.ReadAt(cleartext[sofar:], sofar)
		if err != nil {
			return eio("reading %q (%q): %s", cf.fname, n.uname, err)
		}
		sofar += int64(len)
	}

	// Use the client library to write it back.
	if _, err = cf.c.client.Put(n.uname, cleartext); err != nil {
		return eio("writing back %s (%q): %s", cf.fname, n.uname, err)
	}

	return nil
}

func fingerprint(loc upspin.Location) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(loc.Reference)))
}
