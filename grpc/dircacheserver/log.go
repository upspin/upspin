// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

// This file defines and implements a clogImpl for the directory cache.
// TODO(p): this is currently a write though cache.  It will need work
// to become a write back cache.

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	ospath "path"
	"strings"
	"sync"
	"time"

	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// request is the requested operation to be performed on the DirEntry.
type request int

const (
	lookupReq request = iota
	globReq
	deleteReq
	putReq
	deleteReqDone // will be used for write back
	putDoneReq    // will be used for write back
	maxReq
)

// clogEntry corresponds to an cached operation.
type clogEntry struct {
	request request
	name    upspin.PathName
	error   error
	entries []*upspin.DirEntry
	expires time.Time
}

type clog interface {
	// Close the log and kill any in flight requests.
	close()

	// logRequest logs a request (other than GlobReq) and its reply.
	logRequest(op request, name upspin.PathName, err error, de *upspin.DirEntry)

	// lookup returns a logged entry for name, if there is one, nil otherswise.
	lookup(name upspin.PathName) *clogEntry

	// logGlobRequest logs a Glob request and its reply only if the pattern
	// ends in /* and contains no other metacharacters.
	logGlobRequest(pattern upspin.PathName, err error, entries []*upspin.DirEntry)

	// lookupGlob returns a logged entry for pattern, with the same constraints as
	// logGlobRequest. It also returns nil if pattern not found.
	lookupGlob(pattern upspin.PathName) *clogEntry
}

// clogImpl represents the clogImpl of DirEntry changes. It is primarily used by
// Tree (provided through its Config struct) to clogImpl changes.
type clogImpl struct {
	sync.Mutex
	dir     string      // directory clogImpl lives in
	file    *os.File    // file descriptor for the clogImpl
	offset  int64       // last position appended to the clogImpl (end of clogImpl)
	lru     *cache.LRU  // map from name to clogEntry
	glru    *cache.LRU  // map from name to glob request clogEntry's
	refresh refreshHeap // refresh priority queue
}

// Max number of entries in the LRU.
const LRUMax = 100000

// openLog reads the current log and starts a new one.
// We return an error only if we couldn't create a new log.
func openLog(dir string) (clog, error) {
	const op = "grpc/dircacheserver/openLog"

	l := &clogImpl{dir: dir, lru: cache.NewLRU(LRUMax), glru: cache.NewLRU(LRUMax / 5)}

	fn := ospath.Join(dir, "dircache.clogImpl")
	tfn := fn + ".tmp"

	// Create a new log file.
	if err := l.createLogFile(tfn); err != nil {
		// We can't recover from this.
		return nil, err
	}

	// Open the old log file.  If one didn't exist, just rename the new log file and return.
	f, err := os.Open(fn)
	if err != nil {
		if !os.IsNotExist(err) {
			// if we can't read the old log file, try renaning it for someone to look at.
			if err := os.Rename(fn, fn+".unreadable"); err != nil {
				log.Error.Printf("%s: %s", op, err)
				return nil, errors.E(op, err)
			}
		}
		if err := os.Rename(tfn, fn); err != nil {
			// if we can't rename the new log file, give up.
			log.Error.Printf("%s: %s", op, err)
			return nil, errors.E(op, err)
		}
		return l, nil
	}
	defer f.Close()

	// At this point we have the old file open as 'f' and the new one as 'l.file'.

	// Read as much as we can into memory. A bad read is treated as the end of the log.
	rd := bufio.NewReader(f)
	for {
		var e clogEntry
		if err := e.read(rd); err != nil {
			if err == io.EOF {
				break
			}
			log.Info.Printf("%s: %s", op, err)
			l.lru = cache.NewLRU(LRUMax)
			break
		}
		l.appendToLRU(&e)
	}

	// Write the resulting LRU to the new log. This is a comprequest step since
	// since it should be shorter than the original log: subsequent entries for
	// the same name replace previous ones, deleteReq's remove entries, etc..
	olru := l.lru
	l.lru = cache.NewLRU(LRUMax)
	for {
		_, ev := olru.RemoveOldest()
		if ev == nil {
			break
		}
		e := ev.(*clogEntry)
		if err := l.append(e); err != nil {
			// If we can't append to the new log, perhaps we can continue by
			// using the old.
			l.file.Close()
			if err := os.Remove(tfn); err != nil {
				log.Info.Printf("%s: removing temporary file: %s", op, err)
			}
			if l.file, err = os.OpenFile(fn, os.O_WRONLY|os.O_APPEND, 0600); err != nil {
				log.Error.Printf("%s: %s", op, err)
				return nil, errors.E(op, err)
			}
			return l, nil
		}
	}

	// Rename the new file to the actual name.
	if err := os.Rename(tfn, fn); err != nil {
		log.Error.Printf("%s: %s", op, err)
		return nil, errors.E(op, err)
	}

	return l, nil
}

func (l *clogImpl) close() {
	const op = "grpc/dircacheserver/openLog"

	if err := l.file.Close(); err != nil {
		log.Error.Printf(op, err)
	}
}

func (l *clogImpl) logRequest(op request, name upspin.PathName, err error, de *upspin.DirEntry) {
	if plumbingError(err) {
		return
	}
	e := &clogEntry{request: op, name: name, error: err}
	if de != nil {
		e.entries = append(e.entries, de)
	}
	l.append(e)
}

func (l *clogImpl) lookup(name upspin.PathName) *clogEntry {
	v, ok := l.lru.Get(name)
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

func (l *clogImpl) logGlobRequest(name upspin.PathName, err error, entries []*upspin.DirEntry) {
	dir := patternToDir(name)
	if len(dir) == 0 {
		return
	}
	// Don't cache plumbing errors.
	if plumbingError(err) {
		return
	}
	e := &clogEntry{request: globReq, name: dir, error: err, entries: entries}
	l.append(e)
}

func (l *clogImpl) lookupGlob(name upspin.PathName) *clogEntry {
	dir := patternToDir(name)
	if len(dir) == 0 {
		return nil
	}
	v, ok := l.lru.Get(dir)
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

func (l *clogImpl) createLogFile(fn string) (err error) {
	const op = "grpc/dircacheserver/createLogFile"

	os.Remove(fn)
	l.file, err = os.OpenFile(fn, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
		return errors.E(op, err)
	}
	return nil
}

// appendToLRU adds the entry to the in core LRU version of the clogImpl.
func (l *clogImpl) appendToLRU(e *clogEntry) {
	switch e.request {
	case deleteReq:
		if e.error == nil {
			l.updateGlob(e)
			l.lru.Remove(e.name)
		}
		if e.error == upspin.ErrFollowLink {
			l.lru.Add(e.name, e)
		}
	case putReq:
		if e.error == nil || e.error == upspin.ErrFollowLink {
			l.updateGlob(e)
			l.lru.Add(e.name, e)
		}
	case globReq:
		l.updateGlob(e)
		l.glru.Add(e.name, e)
	default:
		l.updateGlob(e)
		l.lru.Add(e.name, e)
	}
}

// parent returns the parent of the pathname.
func (l *clogImpl) parent(p upspin.PathName) *clogEntry {
	v, ok := l.glru.Get(path.DropPath(p, 1))
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

// updateGlob keeps lru and glru in sync.
func (l *clogImpl) updateGlob(e *clogEntry) {
	switch e.request {
	case globReq:
		// Cache each individual entry in the glob as if it were a lookup request.
		for _, de := range e.entries {
			if !de.IsDir() && len(de.Blocks) == 0 {
				// We had list but not read, don't cache.
				continue
			}
			le := &clogEntry{request: lookupReq, name: de.Name, entries: []*upspin.DirEntry{de}}
			l.lru.Add(e.name, le)
		}
	case putReq, lookupReq:
		// Update any existing glob entry that should include this entry.
		parent := l.parent(e.name)
		if parent == nil {
			return
		}
		for i, de := range parent.entries {
			// Already in the parent?
			if de.Name == e.name {
				if e.error == nil || e.error == upspin.ErrFollowLink {
					// If not an error, replace.
					parent.entries[i] = e.entries[0]
				} else {
					// Otherwise, remove.
					parent.entries = append(parent.entries[:i], parent.entries[i+1:]...)
				}
				return
			}
		}
		// Add to the parent.
		parent.entries = append(parent.entries, e.entries[0])
	case deleteReq:
		// Remove from any existing glob entry.
		parent := l.parent(e.name)
		if parent == nil {
			return
		}
		for i, de := range parent.entries {
			if de.Name == e.name {
				parent.entries = append(parent.entries[:i], parent.entries[i+1:]...)
				return
			}
		}
	}
}

// appendToLogFile appends to the clogImpl file.
func (l *clogImpl) appendToLogFile(e *clogEntry) error {
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	// Wrap with a count.
	buf = appendBytes(nil, buf)

	// Always write to the end of the file.
	if _, err = l.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	if _, err = l.file.Write(buf); err != nil {
		return err
	}
	return nil
}

// append appends a clogEntry to the end of the clogImpl and replaces existing in the LRU.
func (l *clogImpl) append(e *clogEntry) error {
	const op = "grpc/dircacheserver/append"

	l.Lock()
	defer l.Unlock()
	if err := l.appendToLogFile(e); err != nil {
		return errors.E(op, errors.IO, err)
	}
	l.appendToLRU(e)
	return nil
}

func plumbingError(err error) bool {
	if err == nil {
		return false
	}
	if errs, ok := err.(*errors.Error); ok {
		switch errs.Kind {
		case errors.Permission, errors.Exist, errors.NotExist, errors.IsDir, errors.NotDir, errors.NotEmpty:
			return false
		default:
			return true
		}
	}
	return err != upspin.ErrFollowLink
}

// marshal packs the clogEntry into a new byte slice for storage.
func (e *clogEntry) marshal() ([]byte, error) {
	if e.request >= maxReq {
		return nil, errors.Errorf("unknown clogImpl operation %d", e.request)
	}
	b := []byte{byte(e.request)}
	b = appendString(b, string(e.name))
	b = appendBytes(b, errors.MarshalErrorAppend(e.error, nil))
	for _, entry := range e.entries {
		var err error
		b, err = entry.MarshalAppend(b)
		if err != nil {
			return nil, err
		}

	}
	return b, nil
}

var ErrUnmarshal = errors.E(errors.IO, errors.Errorf("unmarshal error"))

// unmarshal unpacks the clogEntry from the byte slice. It unpacks into the receiver
// and returns the remaining bytes, and any error encountered.
func (e *clogEntry) unmarshal(b []byte) error {
	// Get the operation.
	e.request = request(b[0])
	if e.request >= maxReq {
		return errors.E(errors.Invalid, errors.Errorf("unknown clogImpl operation %d", e.request))
	}
	b = b[1:]

	bytes, b := getBytes(b)
	if len(b) < 1 {
		return errors.E(errors.Invalid, errors.Errorf("missing path name"))
	}
	e.name = upspin.PathName(bytes)

	// Get a possible error.
	marshaledError, b := getBytes(b)
	e.error = errors.UnmarshalError(marshaledError)

	// What remains is a slice of Direntries.
	for len(b) > 0 {
		var d upspin.DirEntry
		var err error
		if b, err = d.Unmarshal(b); err != nil {
			return errors.E(errors.Invalid, errors.Str("unmashaling clogEntry"), err)
		}
		e.entries = append(e.entries, &d)
	}
	return nil
}

// read reads a single entry from the clogImpl and unmarshals it.
func (e *clogEntry) read(rd *bufio.Reader) error {
	n, err := binary.ReadVarint(rd)
	if err != nil {
		return err
	}

	b := make([]byte, n)
	m, err := rd.Read(b)
	if err != nil {
		return err
	}
	if m != len(b) {
		return upspin.ErrTooShort
	}
	if err := e.unmarshal(b); err != nil {
		return err
	}
	return nil
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(bytes)))
	b = append(b, tmp[:n]...)
	b = append(b, bytes...)
	return b
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(str)))
	b = append(b, tmp[:n]...)
	b = append(b, str...)
	return b
}

func getBytes(b []byte) (data, remaining []byte) {
	u, n := binary.Varint(b)
	if n == 0 || len(b) < n+int(u) {
		return nil, nil
	}
	return getNBytes(b[n:], int(u))
}

func getNBytes(b []byte, n int) (data, remaining []byte) {
	if len(b) < n {
		return nil, nil
	}
	return b[:n], b[n:]
}

// patternToDir returns the directory represented by a globReq ending in /* and with
// no other metacharacters.
func patternToDir(path upspin.PathName) upspin.PathName {
	dir := strings.TrimSuffix(string(path), "/*")
	if dir == string(path) {
		return upspin.PathName("")
	}
	// This test ignores backslashes so it may be too conservative.
	if strings.ContainsAny(dir, "*?[") {
		return upspin.PathName("")
	}
	return upspin.PathName(dir)
}

// refreshHeap is a priority queue of entries to refresh. The priority is
// the expiration time. It allows entries to be refreshed in the background
// increasing bandwidth but decreasing wait times. To keep the
type refreshHeap struct {
	heap   []*clogEntry
	top    int
	bottom int
}

func newRefreshHeap(len int) *refreshHeap {
	return &refreshHeap{heap: make([]*clogEntry, len)}
}

func (h *refreshHeap) Len() int {
	rv := h.top - h.bottom
	if rv < 0 {
		rv += len(h.heap)
	}
	return rv
}

func (h *refreshHeap) Less(i, j int) bool {
	rv := h.heap[j].expires.After(h.heap[i].expires)
	return rv
}

func (h *refreshHeap) Swap(i, j int) {
	h.heap[i], h.heap[j] = h.heap[j], h.heap[i]
}

func (h *refreshHeap) Push(x interface{}) {
	h.top = (h.top + 1) % len(h.heap)
	if h.top == h.bottom {
		h.bottom = (h.bottom + 1) % len(h.heap)
	}
	h.heap[h.top] = x.(*clogEntry)
}

func (h *refreshHeap) Pop() interface{} {
	if h.Len() == 0 {
		return nil
	}
	h.bottom = (h.bottom + 1) % len(h.heap)
	rv := h.heap[h.bottom]
	h.heap[h.bottom] = nil
	return rv
}
