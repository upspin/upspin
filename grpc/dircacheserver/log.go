// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

// This file defines and implements a replayable log for the directory cache.
//
// Cache entries are kept an fixed size LRU. Therefore, we do not maintain a
// directory tree for each user directory. Instead, we keep log entries containing
// individual DirEntries.
//
// As an optimization we also keep log entries (request = globReq) for directories
// that contain the names of contained files. These entries can be complete, i.e.,
// contain the complete set of file names in the directory or incomplete. The former exists
// when we have seen a Glob("*") request or a Put() of a directory. The latter
// is built as we Lookup or create files.
//
// The view presented is subjectively consistent in that any operation a user
// performs through the cache is consistently represented back to the user. However,
// consistency with the actual directory being cached provides only eventual
// consistency. This consistency is implemented by the refresh go routine which will
// periodicly refresh all entries. The refresh increases if the entry is unchanged
// reflecting file inertia.
//
// We store in individual globReq entries, the pertinent access file if any. This is
// updated as we learn more about access files through Glob, Put, Lookup, Delete or
// WhichAccess. Since we maintain an LRU of known DirEntries rather than a tree, we
// just run the LRU whenever an Access file is added or removed to flush any stale
// entries.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	ospath "path"
	"strings"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// notExist is used to match against returned errors.
var notExist = errors.E(errors.NotExist)

// request is the requested operation to be performed on the DirEntry.
type request int

const (
	lookupReq request = iota
	globReq
	deleteReq
	putReq
	whichAccessReq
	maxReq
)

// noAccessFile is used to indicate we did a WhichAccess and it returned no dir entry.
const noAccessFile = "no known access file"

// clogEntry corresponds to an cached operation.
type clogEntry struct {
	request request
	name    upspin.PathName

	// The error returned on a request.
	error error

	// ep is the endpoint used for refreshing an entry.
	// TODO(p): What happens when the client uses a different
	// directory server?
	ep *upspin.Endpoint

	// de doesn't exist on directories that
	// have not been explicitly Lookup'd or contained in
	// Glob's.
	de *upspin.DirEntry

	// The contents of a directory.
	children map[upspin.PathName]bool
	complete bool // true if the children are the complete set

	// For directories, the access file that pertains.
	access upspin.PathName

	// The times are used to determine when to refresh a cached entry.
	changed   time.Time // when entry was set or changed
	refreshed time.Time // when entry was last refreshed
}

// clog represents the replayable log of DirEntry changes.
type clog struct {
	ctx             upspin.Context
	dir             string // directory clog lives in
	refreshPeriod   time.Duration
	file            *os.File   // file descriptor for the log
	lru             *cache.LRU // [lruKey]*clogEntry
	exit            chan bool  // closed to request refresher to die
	refresherExited chan bool  // closed to signal the refresher has or is about to exit

	// accessLock keeps everyone else out when we are traversing the whole LRU to
	// update access files.
	accessLock sync.RWMutex

	// hashLock synchronizes access to the same directory by multiple RPCs. Better
	// performance than a single lock. Not as good as one per directory but dir
	// entries come and go.
	hashLock []sync.Mutex
}

// lruKey is the lru key. glob's are distinguished from other entries because
// the pattern could clash with a name.
type lruKey struct {
	name upspin.PathName
	ep   upspin.Endpoint
	glob bool
}

// Max number of entries in the LRU.
const LRUMax = 100000

func (l *clog) createLogFile(fn string) (err error) {
	const op = "grpc/dircacheserver.createLogFile"
	log.Debug.Printf("%s %s", op, fn)

	os.Remove(fn)
	l.file, err = os.OpenFile(fn, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Error.Printf("%s: %s", op, err)
		return errors.E(op, err)
	}
	return nil
}

// openLog reads the current log and starts a new one.
// We return an error only if we couldn't create a new log.
func openLog(ctx upspin.Context, dir string, period time.Duration) (*clog, error) {
	const op = "grpc/dircacheserver.openLog"
	log.Debug.Printf(op)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	l := &clog{
		ctx:             ctx,
		lru:             cache.NewLRU(LRUMax),
		refreshPeriod:   period,
		exit:            make(chan bool),
		refresherExited: make(chan bool),
		hashLock:        make([]sync.Mutex, 100),
	}

	fn := ospath.Join(dir, "dircache.clog")
	tfn := fn + ".tmp"

	// Create a new log file.
	if err := l.createLogFile(tfn); err != nil {
		// We can't recover from this.
		log.Error.Printf("%s: %s", op, err)
		return nil, err
	}

	// Open the old log file.  If one didn't exist, just rename the new log file and return.
	f, err := os.Open(fn)
	if err != nil {
		if !os.IsNotExist(err) {
			// if we can't read the old log file, try renaning it for someone
			// to look at if they want.
			log.Error.Printf("%s: %s", op, err)
			if err := os.Rename(fn, fn+".unreadable"); err != nil {
				return nil, errors.E(op, err)
			}
		}
		return finish(op, l, tfn, fn)
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
		log.Debug.Printf("%s: %s", op, &e)
		switch e.request {
		case globReq:
			// Since we first log all the contents of a directory before the glob,
			// we need to first add all entries to a manufactured glob entry. Once
			// we read the actual glob entry, we need to find this manufactured
			// glob and mark it complete. If the directory was empty, that glob
			// will not yet exist and we must manufacture it also.
			v, ok := l.lru.Get(lruKey{name: e.name, ep: *e.ep, glob: true})
			if ok {
				e.children = v.(*clogEntry).children
				e.complete = true
			} else {
				e.children = make(map[upspin.PathName]bool)
			}
			l.updateLRU(&e)
		default:
			l.updateLRU(&e)
		}
	}

	// Write the resulting LRU to the new log. This is a compression step since
	// it should be shorter than the original log: subsequent requests for
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
			log.Error.Printf("%s: %s", op, err)
			return nil, errors.E(op, err)
		}
	}

	return finish(op, l, tfn, fn)
}

func finish(op string, l *clog, tfn, fn string) (*clog, error) {
	if err := os.Rename(tfn, fn); err != nil {
		log.Error.Printf("%s: %s", op, err)
		return nil, errors.E(op, err)
	}

	go l.refresher()
	return l, nil
}

func (l *clog) close() error {
	// Stop refresher.
	close(l.exit)
	<-l.refresherExited

	return l.file.Close()
}

func (l *clog) lock(name upspin.PathName) *sync.Mutex {
	dirName := path.DropPath(name, 1)
	var hash uint32
	for _, i := range []byte(dirName) {
		hash = hash*7 + uint32(i)
	}
	lock := &l.hashLock[hash%uint32(len(l.hashLock))]
	l.accessLock.RLock()
	lock.Lock()
	return lock
}

func (l *clog) unlock(lock *sync.Mutex) {
	lock.Unlock()
	l.accessLock.RUnlock()
}

func (l *clog) lookup(ep *upspin.Endpoint, name upspin.PathName) *clogEntry {
	v, ok := l.lru.Get(lruKey{name: name, ep: *ep, glob: false})
	if ok {
		return v.(*clogEntry)
	}

	// Look for a complete globReq. If there is one and it doesn't list
	// this name, we can return a NotExist error.
	dirName := path.DropPath(name, 1)
	v, ok = l.lru.Get(lruKey{name: dirName, ep: *ep, glob: true})
	if !ok {
		return nil
	}
	e := v.(*clogEntry)
	if !e.complete {
		return nil
	}
	if e.children[name] {
		// The glob entry contains this but we dropped the actual entry.
		return nil
	}
	// Craft an error and return it.
	return &clogEntry{
		name:    name,
		request: lookupReq,
		error:   errors.E(errors.NotExist, errors.Errorf("%s does not exist", name)),
		ep:      ep,
	}
}

func (l *clog) lookupGlob(ep *upspin.Endpoint, pattern upspin.PathName) (*clogEntry, []*upspin.DirEntry) {
	dirPath, ok := cacheableGlob(pattern)
	if !ok {
		return nil, nil
	}
	// Lookup the glob.
	v, ok := l.lru.Get(lruKey{name: dirPath, ep: *ep, glob: true})
	if !ok {
		return nil, nil
	}
	e := v.(*clogEntry)
	if !e.complete {
		return nil, nil
	}
	// Lookup all the individual entries.  If any are missing, no go.
	var entries []*upspin.DirEntry
	for n := range e.children {
		v, ok := l.lru.Get(lruKey{name: n, ep: *ep, glob: false})
		if !ok {
			e.complete = false
			return nil, nil
		}
		ce := v.(*clogEntry)
		if ce.error != nil || ce.de == nil {
			e.complete = false
			return nil, nil
		}
		entries = append(entries, ce.de)
	}
	return v.(*clogEntry), entries
}

func (l *clog) whichAccess(ep *upspin.Endpoint, name upspin.PathName) (*upspin.DirEntry, bool) {
	// Get name of access file.
	dirName := path.DropPath(name, 1)
	v, ok := l.lru.Get(lruKey{name: dirName, ep: *ep, glob: true})
	if !ok {
		return nil, false
	}

	// See if we have a directory entry for it.
	e := v.(*clogEntry)
	if len(e.access) == 0 {
		return nil, false
	}
	if e.access == noAccessFile {
		return nil, true
	}
	v, ok = l.lru.Get(lruKey{name: e.access, ep: *e.ep})
	if !ok {
		return nil, false
	}
	e = v.(*clogEntry)
	return e.de, true
}

func (l *clog) logRequest(op request, ep *upspin.Endpoint, name upspin.PathName, err error, de *upspin.DirEntry) {
	if !cacheableError(err) {
		return
	}
	e := &clogEntry{
		name:    name,
		request: op,
		error:   err,
		ep:      ep,
		de:      de,
	}
	l.append(e)

	// Optimization: when creating a directory, fake a Glob log entry since
	// we know that the directory is empty and don't have to ask the server.
	if op == putReq && err == nil && de != nil && de.IsDir() {
		e := &clogEntry{
			name:     name,
			request:  globReq,
			error:    err,
			ep:       ep,
			children: make(map[upspin.PathName]bool),
			complete: true,
		}
		l.append(e)
	}
}

// cacheableGlob returns the path minus the /* and true if the pattern corresponds to a discrete directory listing.
func cacheableGlob(p upspin.PathName) (upspin.PathName, bool) {
	if !strings.HasSuffix(string(p), "/*") {
		return p, false
	}
	pp := path.DropPath(p, 1)

	// This test also rejects globs with escaped glob characters, i.e., real glob
	// characters in file names.
	if strings.IndexAny(string(pp), "*?[") >= 0 {
		return p, false
	}
	return pp, true
}

func (l *clog) logGlobRequest(ep *upspin.Endpoint, pattern upspin.PathName, err error, entries []*upspin.DirEntry) {
	if !cacheableError(err) {
		return
	}
	dirName, ok := cacheableGlob(pattern)
	if !ok {
		return
	}

	// Log each entry.
	children := make(map[upspin.PathName]bool)
	for _, de := range entries {
		children[de.Name] = true
		// TODO(p): Put check for incomplete here.
		l.logRequest(lookupReq, ep, de.Name, err, de)
	}

	// If any files have disappeared from a preexisting glob, remove them.
	v, ok := l.lru.Get(lruKey{name: dirName, ep: *ep, glob: true})
	if ok {
		oe := v.(*clogEntry)
		for n := range oe.children {
			if !children[n] {
				e := &clogEntry{request: deleteReq, ep: ep, name: n}
				l.append(e)
			}
		}
	}

	// Log the glob itself.
	e := &clogEntry{
		request:  globReq,
		name:     dirName,
		error:    err,
		ep:       ep,
		children: children,
		complete: true,
	}
	l.append(e)
}

// append appends a clogEntry to the end of the clog and replaces existing in the LRU.
func (l *clog) append(e *clogEntry) error {
	const op = "grpc/dircacheserver.append"

	if err := l.appendToLogFile(e); err != nil {
		return errors.E(op, errors.IO, err)
	}
	l.updateLRU(e)
	return nil
}

// updateLRU adds the entry to the in core LRU version of the clog. We don't remember errors
// in the LRU (other than ErrFollowLink). However, we do use them to remove things from the LRU.
func (l *clog) updateLRU(e *clogEntry) {
	if e.error != nil {
		// Remember links.  All cases are equivalent, i.e., treat them like a lookup.
		if e.error == upspin.ErrFollowLink {
			e.request = lookupReq
			l.addToLRU(e, false)
			l.addToGlob(e)
			return
		}
		if !errors.Match(notExist, e.error) {
			return
		}
		// Remove from everywhere possible.
		k := lruKey{name: e.name, ep: *e.ep, glob: e.request == globReq}
		l.lru.Remove(k)
		l.removeFromGlob(e)
		l.removeAccess(e)
		// Add back in as a non-existent file.
		e.request = lookupReq
		l.addToLRU(e, true)
		return
	}

	// At this point, only requests have gotten through.
	switch e.request {
	case deleteReq:
		l.lru.Remove(lruKey{name: e.name, ep: *e.ep, glob: true})
		l.lru.Remove(lruKey{name: e.name, ep: *e.ep, glob: false})
		l.removeFromGlob(e)
		l.removeAccess(e)
	case putReq:
		l.addToLRU(e, true)
		l.addToGlob(e)
		l.addAccess(e)
	case lookupReq, globReq:
		l.addToLRU(e, false)
		l.addToGlob(e)
		l.addAccess(e)
	case whichAccessReq:
		// Log the access file itself as a lookup.
		if e.de != nil {
			ae := &clogEntry{
				request: lookupReq,
				name:    e.de.Name,
				error:   nil,
				ep:      e.ep,
				de:      e.de,
			}
			l.addAccess(ae)
		}

		// Add it to the specific entry.
		dirName := path.DropPath(e.name, 1)
		v, ok := l.lru.Get(lruKey{name: dirName, ep: *e.ep, glob: true})
		if ok {
			if e.de != nil {
				v.(*clogEntry).access = e.de.Name
			} else {
				v.(*clogEntry).access = noAccessFile
			}
		}
	default:
		log.Printf("unknown request type: %s", e)
	}
}

// addToLRU adds an entry to the LRU updating times used for refresh.
func (l *clog) addToLRU(e *clogEntry, changed bool) {
	e.refreshed = time.Now()
	e.changed = e.refreshed
	k := lruKey{name: e.name, ep: *e.ep, glob: e.request == globReq}
	if !changed {
		if v, ok := l.lru.Get(k); ok {
			oe := v.(*clogEntry)
			if match(e, oe.de, oe.error) {
				// Don't update change time if nothing has changed.
				// This is strictly for refresh decision.
				e.changed = oe.changed
			}
		}
	}
	l.lru.Add(k, e)
}

// addToGlob creates the glob if it doesn't exist and adds an entry to it.
func (l *clog) addToGlob(e *clogEntry) {
	dirName := path.DropPath(e.name, 1)
	if dirName == "" {
		return
	}
	var ge *clogEntry
	k := lruKey{name: dirName, ep: *e.ep, glob: true}
	if v, ok := l.lru.Get(k); !ok {
		ge = &clogEntry{
			request:  globReq,
			name:     dirName,
			ep:       e.ep,
			children: make(map[upspin.PathName]bool),
			complete: false,
		}
		l.lru.Add(k, ge)
	} else {
		ge = v.(*clogEntry)
	}
	ge.children[e.name] = true
}

// removeFromGlob removes an entry from a glob, should that glob exist.
func (l *clog) removeFromGlob(e *clogEntry) {
	dirName := path.DropPath(e.name, 1)
	k := lruKey{name: dirName, ep: *e.ep, glob: true}
	if v, ok := l.lru.Get(k); ok {
		ge := v.(*clogEntry)
		if ge.children[e.name] {
			delete(ge.children, e.name)
		}
	}
}

// addAccess adds an access pointer to its eirectory and removes one
// from all descendant directories that point to an ascendant of the
// access file's directory.
func (l *clog) addAccess(e *clogEntry) {
	if !access.IsAccessFile(e.name) {
		return
	}
	l.accessLock.RUnlock()
	l.accessLock.Lock()

	// Add the access to its immediate directory.
	dirName := path.DropPath(e.name, 1)
	v, ok := l.lru.Get(lruKey{name: dirName, ep: *e.ep, glob: true})
	if ok {
		v.(*clogEntry).access = e.name
	}

	// Remove the access file for any descendant that points at an ascendant.
	iter := l.lru.NewIterator()
	for {
		_, v, ok := iter.GetAndAdvance()
		if !ok {
			break
		}
		ne := v.(*clogEntry)
		if ne.request != globReq {
			continue
		}
		if !strings.HasPrefix(string(ne.name), string(dirName)) {
			continue
		}
		if len(ne.access) < len(e.name) {
			ne.access = ""
		}
	}
	l.accessLock.Unlock()
	l.accessLock.RLock()
}

// removeAccess removes an access pointer from its directory and removes it
// from any descendant directory.
func (l *clog) removeAccess(e *clogEntry) {
	if !access.IsAccessFile(e.name) {
		return
	}
	l.accessLock.RUnlock()
	l.accessLock.Lock()

	// Remove the access from its immediately directory.
	dirName := path.DropPath(e.name, 1)
	v, ok := l.lru.Get(lruKey{name: dirName, ep: *e.ep, glob: true})
	if ok {
		v.(*clogEntry).access = ""
	}

	// Remove this access file from any descendant.
	iter := l.lru.NewIterator()
	for {
		_, v, ok := iter.GetAndAdvance()
		if !ok {
			break
		}
		ne := v.(*clogEntry)
		if ne.request != globReq {
			continue
		}
		if !strings.HasPrefix(string(ne.name), string(dirName)) {
			continue
		}
		if ne.access == e.name {
			ne.access = ""
		}
	}
	l.accessLock.Unlock()
	l.accessLock.RLock()
}

// appendToLogFile appends to the clog file.
func (l *clog) appendToLogFile(e *clogEntry) error {
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	// Wrap with a count.
	buf = appendBytes(nil, buf)

	_, err = l.file.Write(buf)
	return err
}

// cacheableError returns true if there is no error of if the error is one we can live with.
func cacheableError(err error) bool {
	if err == nil {
		return true
	}
	if e, ok := err.(*errors.Error); ok {
		return errors.Match(notExist, e)
	}
	return err == upspin.ErrFollowLink
}

var tooShort = errors.E(errors.Invalid, errors.Errorf("log entry too short"))
var tooLong = errors.E(errors.Invalid, errors.Errorf("log entry too long"))

// marshal packs the clogEntry into a new byte slice for storage.
func (e *clogEntry) marshal() ([]byte, error) {
	if e.request >= maxReq {
		return nil, errors.Errorf("unknown clog operation %d", e.request)
	}
	var complete int
	if e.complete {
		complete = 1
	}
	b := []byte{byte(e.request), byte(complete), byte(e.ep.Transport)}
	b = appendString(b, string(e.ep.NetAddr))
	b = appendString(b, string(e.name))
	b = appendError(b, e.error)
	return appendDirEntry(b, e.de)
}

// unmarshal unpacks the clogEntry from the byte slice. It unpacks into the receiver
// and returns any error encountered.
func (e *clogEntry) unmarshal(b []byte) (err error) {
	if len(b) < 3 {
		return tooShort
	}
	e.request = request(b[0])
	if e.request >= maxReq {
		return errors.E(errors.Invalid, errors.Errorf("unknown clog operation %d", e.request))
	}
	e.complete = int(b[1]) != 0
	e.ep = &upspin.Endpoint{}
	e.ep.Transport = upspin.Transport(b[2])
	var str string
	if str, b, err = getString(b[3:]); err != nil {
		return err
	}
	e.ep.NetAddr = upspin.NetAddr(str)
	if str, b, err = getString(b); err != nil {
		return err
	}
	e.name = upspin.PathName(str)
	if e.error, b, err = getError(b); err != nil {
		return err
	}
	if e.de, b, err = getDirEntry(b); err != nil {
		return err
	}
	if len(b) != 0 {
		return errors.E(errors.Invalid, errors.Errorf("log entry too long"))
	}
	return
}

// read reads a single entry from the clog and unmarshals it.
func (e *clogEntry) read(rd *bufio.Reader) error {
	n, err := binary.ReadVarint(rd)
	if err != nil {
		return err
	}

	b := make([]byte, n)
	sofar := 0
	for {
		m, err := rd.Read(b[sofar:])
		if err != nil {
			return err
		}
		sofar += m
		if int64(sofar) == n {
			break
		}
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

func getBytes(b []byte) (data, remaining []byte, err error) {
	u, n := binary.Varint(b)
	if n == 0 {
		return nil, b, nil
	}
	b = b[n:]
	if len(b) < int(u) {
		return nil, nil, tooShort
	}
	return b[:u], b[u:], nil
}

func appendString(b []byte, str string) []byte {
	return appendBytes(b, []byte(str))
}

func getString(b []byte) (str string, remaining []byte, err error) {
	var bytes []byte
	if bytes, remaining, err = getBytes(b); err != nil {
		return "", nil, err
	}
	return string(bytes), remaining, nil
}

func appendDirEntry(b []byte, de *upspin.DirEntry) ([]byte, error) {
	if de == nil {
		return appendBytes(b, nil), nil
	}
	bytes, err := de.Marshal()
	if err != nil {
		return b, err
	}
	return appendBytes(b, bytes), nil
}

func getDirEntry(b []byte) (de *upspin.DirEntry, remaining []byte, err error) {
	bytes, remaining, err := getBytes(b)
	if err != nil || len(bytes) == 0 {
		return
	}
	de = &upspin.DirEntry{}
	x, err := de.Unmarshal(bytes)
	if len(x) != 0 {
		return nil, nil, tooLong
	}
	return
}

func appendError(b []byte, err error) []byte {
	return appendBytes(b, errors.MarshalErrorAppend(err, nil))
}

func getError(b []byte) (wrappedErr error, remaining []byte, err error) {
	bytes, remaining, err := getBytes(b)
	if err != nil {
		return nil, nil, err
	}
	if bytes == nil {
		return
	}
	wrappedErr = errors.UnmarshalError(bytes)
	// Hack to make all the direct comparisons work.
	if wrappedErr != nil && wrappedErr.Error() == upspin.ErrFollowLink.Error() {
		wrappedErr = upspin.ErrFollowLink
	}
	return
}

const maxRefreshPerRound = 50 // The max number of refreshes we will perform per refresh round

// refresher is a goroutine that refreshes entries in the LRU.  It does this in
// bursts to make life easier on connections that have a long setup time, such as
// cellular links.
//
// The longer an entry has gone since changing, the longer the refresh period. The
// assumption is that the longer since something has changed, the longer it will be
// till it is next changed.
//
// TODO(p): This is far from an optimal refresh policy. We could use the merkle structure
// of the directory to do better. A future CL.
func (l *clog) refresher() {
	iter := l.lru.NewIterator()
	gIter := l.lru.NewIterator()
	for {
		// Each round we keep track of what connections failed so that we
		// we don't waste time retrying them.
		failed := make(map[upspin.Endpoint]bool)

		// First sweep globs since they will also fix up single entries.
		gIter = l.refreshLoop(gIter, failed, true)
		iter = l.refreshLoop(iter, failed, false)

		select {
		case <-l.exit:
			close(l.refresherExited)
			return
		case <-time.After(l.refreshPeriod):
		}
	}
}

const maxRefreshPeriod = time.Hour

// refreshLoop iterates through the LRU refreshing as it goes. It terminates either when it reaches
// the end of the LRU, has refreshed maxRefreshPerRound entries, or is told to die.
func (l *clog) refreshLoop(iter *cache.Iterator, failed map[upspin.Endpoint]bool, globOnly bool) *cache.Iterator {
	for n := 0; n < maxRefreshPerRound; {
		select {
		case <-l.exit:
			return iter
		default:
		}

		_, v, ok := iter.GetAndAdvance()
		if !ok {
			return l.lru.NewIterator()
		}
		e := v.(*clogEntry)

		// Avoid any destinations we couldn't reach this round.
		if failed[*e.ep] {
			continue
		}

		if (globOnly && e.request != globReq) || (!globOnly && e.request == globReq) {
			continue
		}

		lock := l.lock(e.name)
		sinceChanged := time.Since(e.changed)
		sinceRefreshed := time.Since(e.refreshed)
		l.unlock(lock)
		if 2*sinceRefreshed > sinceChanged || sinceRefreshed > maxRefreshPeriod {
			if l.refresh(e) {
				log.Debug.Printf("dircacheserver: refresh %s OK\n", e.name)
				n++
			} else {
				log.Debug.Printf("dircacheserver: refresh %s failed\n", e.name)
				failed[*e.ep] = true
			}
		}
	}
	return iter
}

// refresh refreshes a single entry. Returns true if the refresh happened.
func (l *clog) refresh(e *clogEntry) bool {
	dir, err := bind.DirServer(l.ctx, *e.ep)
	if err != nil {
		// plumbing problem
		return false
	}
	lock := l.lock(e.name)
	defer l.unlock(lock)
	switch e.request {
	case globReq:
		entries, err := dir.Glob(string(e.name))
		if !cacheableError(err) {
			return false
		}
		l.logGlobRequest(e.ep, e.name, err, entries)
	default:
		de, err := dir.Lookup(e.name)
		if !cacheableError(err) {
			return false
		}
		l.logRequest(lookupReq, e.ep, e.name, err, de)
	}
	return true
}

// match matches a log entry to the return from a lookup.
func match(e *clogEntry, de *upspin.DirEntry, err error) bool {
	if de != nil {
		if e.de == nil {
			return false
		}
		if e.de.Sequence != de.Sequence {
			return false
		}
	}
	return matchErrors(e.error, err)
}

// matchGlob matches a glob entry to the return from a Glob.
func (l *clog) matchGlob(e *clogEntry, entries []*upspin.DirEntry, err error) bool {
	if len(e.children) != len(entries) {
		return false
	}
	if !matchErrors(e.error, err) {
		return false
	}
	for _, de := range entries {
		found := false
		if !e.children[de.Name] {
			return false
		}
		v, ok := l.lru.Get(lruKey{name: de.Name, ep: *e.ep, glob: false})
		if !ok {
			return false
		}
		ee := v.(*clogEntry)
		if ee.de == nil || ee.de.Sequence != de.Sequence {
			return false
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

var reqName = map[request]string{
	lookupReq:      "lookup",
	globReq:        "glob",
	deleteReq:      "delete",
	putReq:         "put",
	whichAccessReq: "whichAccess",
}

func (e *clogEntry) String() string {
	rv := "?"
	if e.request >= lookupReq && e.request < maxReq {
		rv = reqName[e.request]
	}
	rv += fmt.Sprintf(" %s ep<%v>", e.name, *e.ep)
	if e.error != nil {
		rv += fmt.Sprintf(" error<%s>", e.error)
	}
	if e.de != nil {
		rv += fmt.Sprintf(" de<%s, %s, %d>", e.de.Name, e.de.Link, e.de.Sequence)
	}
	if e.children != nil {
		rv += fmt.Sprintf(" children<%v>", e.children)
	}
	if e.complete {
		rv += " complete"
	}
	return rv
}
