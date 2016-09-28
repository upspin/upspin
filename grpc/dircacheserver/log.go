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

	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
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

	// Only for glob requests are there more than one entry.
	entries []*upspin.DirEntry

	// The endpoint used for refreshing an entry.
	// TODO(p): What happens when the client uses a different
	// directory server?
	ep *upspin.Endpoint

	// The times are used to determine when to refresh a cached entry.
	changed   time.Time // when entry was set or changed
	refreshed time.Time // when entry was last refreshed
}

type clog interface {
	// Close the log and kill any in flight requests.
	close()

	// lookup returns a logged entry for name, if there is one, nil otherswise.
	lookup(e *upspin.Endpoint, name upspin.PathName) *clogEntry

	// lookup returns a logged entry for glob pattern, if there is one, nil otherswise.
	lookupGlob(e *upspin.Endpoint, name upspin.PathName) *clogEntry

	// logRequest logs a request (other than GlobReq) and its reply.
	logRequest(op request, e *upspin.Endpoint, name upspin.PathName, err error, de *upspin.DirEntry)

	// logGlobRequest logs a Glob request and its reply only if the pattern
	// ends in /* and contains no other metacharacters.
	logGlobRequest(e *upspin.Endpoint, pattern upspin.PathName, err error, entries []*upspin.DirEntry)
}

// clogImpl represents the clogImpl of DirEntry changes. It is primarily used by
// Tree (provided through its Config struct) to clogImpl changes.
type clogImpl struct {
	sync.Mutex
	ctx           upspin.Context
	dir           string // directory clogImpl lives in
	refreshPeriod time.Duration
	file          *os.File   // file descriptor for the clogImpl
	offset        int64      // last position appended to the clogImpl (end of clogImpl)
	lru           *cache.LRU // map from name to clogEntry
	timeToDie     chan struct{}
	deathWatch    chan struct{}
}

// lruIndex is the index into the lru. glob's are distinguished from other entries because
// the pattern could clash with a name.
type lruIndex struct {
	name upspin.PathName
	ep   upspin.Endpoint
	glob bool
}

// Max number of entries in the LRU.
const LRUMax = 100000

// openLog reads the current log and starts a new one.
// We return an error only if we couldn't create a new log.
func openLog(ctx upspin.Context, dir string, period time.Duration) (clog, error) {
	const op = "grpc/dircacheserver/openLog"
	os.MkdirAll(dir, 0700)

	l := &clogImpl{
		ctx:           ctx,
		dir:           dir,
		lru:           cache.NewLRU(LRUMax),
		refreshPeriod: period,
		timeToDie:     make(chan struct{}),
		deathWatch:    make(chan struct{}),
	}

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
		go l.refresher()
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
			break
		}
		l.appendToLRU(&e)
	}

	// Write the resulting LRU to the new log. This is a compression step since
	// since it should be shorter than the original log: subsequent reuests for
	// the same name replace previous ones, deleteReq's remove entries, etc.
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

	go l.refresher()
	return l, nil
}

func (l *clogImpl) close() {
	const op = "grpc/dircacheserver/openLog"

	// Stop refrshing.
	close(l.timeToDie)
	<-l.deathWatch

	if err := l.file.Close(); err != nil {
		log.Error.Printf(op, err)
	}
}

func (l *clogImpl) logRequest(op request, ep *upspin.Endpoint, name upspin.PathName, err error, de *upspin.DirEntry) {
	if plumbingError(err) {
		return
	}
	e := &clogEntry{request: op, ep: ep, name: name, error: err}
	if de != nil {
		e.entries = append(e.entries, de)
	}
	l.append(e)
}

func (l *clogImpl) lookup(ep *upspin.Endpoint, name upspin.PathName) *clogEntry {
	v, ok := l.lru.Get(lruIndex{name: name, ep: *ep, glob: false})
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

func (l *clogImpl) lookupGlob(ep *upspin.Endpoint, pattern upspin.PathName) *clogEntry {
	if !okGlob(pattern) {
		return nil
	}
	v, ok := l.lru.Get(lruIndex{name: pattern, ep: *ep, glob: true})
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

func (l *clogImpl) logGlobRequest(ep *upspin.Endpoint, pattern upspin.PathName, err error, entries []*upspin.DirEntry) {
	if !okGlob(pattern) {
		return
	}
	// Don't cache plumbing errors.
	if plumbingError(err) {
		return
	}
	e := &clogEntry{request: globReq, ep: ep, name: pattern, error: err, entries: entries}
	l.append(e)
}

// okGlob returns true if the pattern corresponds to a discrete directory listing.
func okGlob(pattern upspin.PathName) bool {
	p := string(pattern)
	if !strings.HasSuffix(p, "/*") {
		return false
	}
	// overly paranoid but shouldn't hurt
	return strings.IndexAny(p[:len(p)-2], "*?[") < 0
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
	i := lruIndex{name: e.name, ep: *e.ep, glob: e.request == globReq}
	e.changed = time.Now()
	e.refreshed = e.changed
	switch e.request {
	case deleteReq:
		if e.error == nil {
			l.updateGlob(e)
			l.lru.Remove(i)
		}
		if e.error == upspin.ErrFollowLink {
			l.lru.Add(i, e)
		}
	case putReq:
		if e.error == nil || e.error == upspin.ErrFollowLink {
			l.updateGlob(e)
			l.lru.Add(i, e)
		}
	default:
		l.updateGlob(e)
		l.lru.Add(i, e)
	}
}

// parent returns a matching LRU entry for the parent of 'p' or nil if none exists.
func (l *clogImpl) parent(e *clogEntry) *clogEntry {
	pp := string(e.name)
	i := strings.LastIndex(pp, "/")
	if i < 0 {
		return nil
	}

	v, ok := l.lru.Get(lruIndex{name: upspin.PathName(pp[:i+1] + "*"), ep: *e.ep, glob: true})
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

// updateGlob keeps the lru's single and glob entries in sync.
func (l *clogImpl) updateGlob(e *clogEntry) {
	switch e.request {
	case globReq:
		// Cache each individual entry in the glob as if it were a lookup request.
		for _, de := range e.entries {
			if !de.IsDir() && len(de.Blocks) == 0 {
				// We had list but not read, don't cache.
				continue
			}
			le := &clogEntry{
				request:   lookupReq,
				name:      de.Name,
				ep:        e.ep,
				entries:   []*upspin.DirEntry{de},
				refreshed: e.refreshed,
				changed:   e.changed,
			}
			l.lru.Add(lruIndex{name: le.name, ep: *e.ep, glob: false}, le)
		}
	case putReq, lookupReq:
		// Update any existing glob entry that should include this entry.
		parent := l.parent(e)
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
		parent := l.parent(e)
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
		case errors.Permission, errors.Exist, errors.NotExist, errors.IsDir, errors.NotDir, errors.NotEmpty, errors.Invalid:
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
	b = append(b, byte(e.ep.Transport))
	b = appendString(b, string(e.ep.NetAddr))
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

// unmarshal unpacks the clogEntry from the byte slice. It unpacks into the receiver
// and returns the remaining bytes, and any error encountered.
func (e *clogEntry) unmarshal(b []byte) error {
	if len(b) < 3 {
		return upspin.ErrTooShort
	}

	// Get the operation.
	e.request = request(b[0])
	if e.request >= maxReq {
		return errors.E(errors.Invalid, errors.Errorf("unknown clogImpl operation %d", e.request))
	}

	// Get the server endpoint.
	var bytes []byte
	e.ep = &upspin.Endpoint{}
	e.ep.Transport = upspin.Transport(b[1])
	b = b[2:]
	bytes, b = getBytes(b)
	if b == nil {
		return upspin.ErrTooShort
	}
	e.ep.NetAddr = upspin.NetAddr(bytes)

	// Get the pathname.
	bytes, b = getBytes(b)
	if len(bytes) < 1 {
		return errors.E(errors.Invalid, errors.Errorf("missing path name"))
	}
	e.name = upspin.PathName(bytes)

	// Get a possible error.
	marshaledError, b := getBytes(b)
	e.error = errors.UnmarshalError(marshaledError)

	// What remains is a slice of DirEntries.
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

// refresher is a go routine that refreshes entries in the LRU.  It does this in
// bursts to make life easier on connections that have a long setup time, such as
// cellular links.
//
// The longer an entry has gone since changing, the longer the refresh period. The
// assumption is that the longer since something has changed, the longer it will be
// till it is next changed.
//
// TODO(p): This is far from an optimal refresh policy. We could use the merkle structure
// of the directory to do better. A future CL.
func (l *clogImpl) refresher() {
	iter := l.lru.NewIterator()
	gIter := l.lru.NewIterator()
	for {
		// limit the number of refreshes per round.
		const maxRefreshPerRound = 50

		// Each round we keep track of what connections failed so that we
		// we don't waste time retrying them.
		failed := make(map[upspin.Endpoint]struct{})

		// First sweep globs since they will also fix up single entries.
		gIter = l.refreshLoop(maxRefreshPerRound, gIter, failed, true)
		iter = l.refreshLoop(maxRefreshPerRound, iter, failed, false)

		select {
		case <-l.timeToDie:
			close(l.deathWatch)
			return
		case <-time.After(l.refreshPeriod):
		}
	}
}

// refreshLoop iterates through the LRU refreshing as it goes. It terminates either when it reachesd
// the end of the LRU, has refreshde maxRefreshPerRound entries, or is told to die.
func (l *clogImpl) refreshLoop(maxRefreshPerRound int, iter *cache.Iterator, failed map[upspin.Endpoint]struct{}, globOnly bool) *cache.Iterator {
	for n := 0; n < maxRefreshPerRound; {
		select {
		case <-l.timeToDie:
			return iter
		default:
		}

		_, v, ok := iter.GetAndAdvance()
		if !ok {
			return l.lru.NewIterator()
		}
		e := v.(*clogEntry)

		// Avoid any destinations we couldn't reach this round.
		if _, ok := failed[*e.ep]; ok {
			continue
		}

		if (globOnly && e.request != globReq) || (!globOnly && e.request == globReq) {
			continue
		}

		sinceChanged := time.Since(e.changed)
		sinceRefreshed := time.Since(e.refreshed)
		if time.Duration(2.0)*sinceRefreshed > sinceChanged {
			if l.refresh(e) {
				log.Debug.Printf("refresh %s OK\n", e.name)
				n++
			} else {
				log.Debug.Printf("refresh %s failed\n", e.name)
				failed[*e.ep] = struct{}{}
			}
		}
	}
	return iter
}

// refresh refreshes a single entry. Returns true if the refresh happened.
func (l *clogImpl) refresh(e *clogEntry) bool {
	dir, err := bind.DirServer(l.ctx, *e.ep)
	if err != nil {
		// plumbing problem
		return false
	}
	switch e.request {
	case globReq:
		entries, err := dir.Glob(string(e.name))
		if err != nil {
			if plumbingError(err) {
				return false
			}
		}
		if !matchGlob(e, entries, err) {
			ne := &clogEntry{
				name:    e.name,
				request: globReq,
				ep:      e.ep,
				error:   err,
				entries: entries,
			}
			log.Debug.Printf("refreshed %v", *ne)
			l.append(ne)
		} else {
			e.refreshed = time.Now()
		}
	default:
		de, err := dir.Lookup(e.name)
		if err != nil {
			if plumbingError(err) {
				return false
			}
		}
		if !match(e, de, err) {
			ne := &clogEntry{
				name:    e.name,
				request: lookupReq,
				ep:      e.ep,
				error:   err,
				entries: []*upspin.DirEntry{de},
			}
			log.Debug.Printf("refreshed %v", *ne)
			l.append(ne)
		} else {
			e.refreshed = time.Now()
		}
	}
	return true
}

func match(e *clogEntry, de *upspin.DirEntry, err error) bool {
	if de != nil {
		if len(e.entries) != 1 || e.entries[0].Sequence != de.Sequence || e.entries[0].Name != de.Name {
			return false
		}
	} else {
		if len(e.entries) != 0 {
			return false
		}
	}
	return matchErrors(e.error, err)
}

func matchGlob(e *clogEntry, entries []*upspin.DirEntry, err error) bool {
	if len(e.entries) != len(entries) {
		return false
	}
	if !matchErrors(e.error, err) {
		return false
	}
	for _, de := range entries {
		found := false
		for _, ede := range e.entries {
			if ede.Name == de.Name && ede.Sequence == de.Sequence {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchErrors(e1 error, e2 error) bool {
	if e1 == e2 {
		return true
	}
	return errors.Match(e1, e2)
}
