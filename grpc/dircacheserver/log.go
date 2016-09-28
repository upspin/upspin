// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

// This file defines and implements a replayable log for the directory cache.
// TODO(p): this is currently a write though cache.  It will need work
// to become a write back cache.

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

	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
	//"upspin.io/path"
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
	children map[upspin.PathName]struct{}
	complete bool // true if the children are the complete set

	// The times are used to determine when to refresh a cached entry.
	changed   time.Time // when entry was set or changed
	refreshed time.Time // when entry was last refreshed
}

// clog represents the replayable log of DirEntry changes.
type clog struct {
	sync.Mutex

	ctx             upspin.Context
	dir             string // directory clog lives in
	refreshPeriod   time.Duration
	file            *os.File      // file descriptor for the log
	lru             *cache.LRU    // [lruKey]*clogEntry
	exit            chan struct{} // closed to request refresher to die
	refresherExited chan struct{} // closed to signal the refresher has or is about to exit
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
	const op = "grpc/dircacheserver/createLogFile"

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
	const op = "grpc/dircacheserver/openLog"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	l := &clog{
		ctx:             ctx,
		lru:             cache.NewLRU(LRUMax),
		refreshPeriod:   period,
		exit:            make(chan struct{}),
		refresherExited: make(chan struct{}),
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
		log.Info.Printf("openLog read: %s", &e)
		// Since we first log all the contents of a directory before the glob,
		// we need to all all non-glob entries to a manufactured glob. Once we
		// read the glob entry, we need to find this manufactured glob and mark
		// it complete. If the directory was empty, that glob will not yet
		// exist and we must manufacture one.
		if e.request == globReq {
			v, ok := l.lru.Get(lruKey{name: e.name, ep: *e.ep, glob: true})
			if ok {
				v.(*clogEntry).complete = true
			} else {
				l.addToLRU(&e, true)
			}
		} else {
			l.addToLRU(&e, true)
			l.addToGlob(&e)
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

	if err := l.file.Close(); err != nil {
		return err
	}
	return nil
}

func (l *clog) lookup(ep *upspin.Endpoint, name upspin.PathName) *clogEntry {
	v, ok := l.lru.Get(lruKey{name: name, ep: *ep, glob: false})
	if !ok {
		return nil
	}
	return v.(*clogEntry)
}

func (l *clog) lookupGlob(ep *upspin.Endpoint, pattern upspin.PathName) (*clogEntry, []*upspin.DirEntry) {
	dirPath, ok := okGlob(pattern)
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

func (l *clog) logRequest(op request, ep *upspin.Endpoint, name upspin.PathName, err error, de *upspin.DirEntry) {
	if !cacheable(err) {
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
}

// okGlob returns the path minus the /* and true if the pattern corresponds to a discrete directory listing.
func okGlob(path upspin.PathName) (upspin.PathName, bool) {
	p := strings.TrimSuffix(string(path), "/*")
	if p == string(path) {
		return path, false
	}

	// overly paranoid but shouldn't hurt
	if strings.IndexAny(p, "*?[") >= 0 {
		return path, false
	}
	return upspin.PathName(p), true
}

func (l *clog) logGlobRequest(ep *upspin.Endpoint, pattern upspin.PathName, err error, entries []*upspin.DirEntry) {
	if !cacheable(err) {
		return
	}
	dirName, ok := okGlob(pattern)
	if !ok {
		return
	}

	// Log each entry.
	children := make(map[upspin.PathName]struct{})
	for _, de := range entries {
		children[de.Name] = struct{}{}
		// TODO(p): Put check for incomplete here.
		l.logRequest(lookupReq, ep, de.Name, err, de)
	}

	// If any files have disappeared from a preexisting glob, remove them.
	v, ok := l.lru.Get(lruKey{name: dirName, ep: *ep, glob: true})
	if ok {
		oe := v.(*clogEntry)
		for n := range oe.children {
			if _, ok := children[n]; !ok {
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
	const op = "grpc/dircacheserver/append"

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
		// Remember links.
		if e.error == upspin.ErrFollowLink {
			e.request = lookupReq
			l.addToLRU(e, false)
			l.addToGlob(e)
			return
		}
		if !errors.Match(errors.E(errors.NotExist), e.error) {
			return
		}
		// Remember non existent files.
		i := lruKey{name: e.name, ep: *e.ep, glob: e.request == globReq}
		l.lru.Remove(i)
		l.removeFromGlob(e)
		return
	}

	// At this point, only requests have gotten through.
	switch e.request {
	case deleteReq:
		l.lru.Remove(lruKey{name: e.name, ep: *e.ep, glob: true})
		l.lru.Remove(lruKey{name: e.name, ep: *e.ep, glob: false})
		l.removeFromGlob(e)
	case putReq:
		l.addToLRU(e, true)
		l.addToGlob(e)
	case lookupReq:
		fallthrough
	case globReq:
		l.addToLRU(e, false)
		l.addToGlob(e)
	}
}

// addToLRU adds an entry to the LRU updating times used for refresh.
func (l *clog) addToLRU(e *clogEntry, changed bool) {
	e.refreshed = time.Now()
	e.changed = e.refreshed
	i := lruKey{name: e.name, ep: *e.ep, glob: e.request == globReq}
	if !changed {
		if v, ok := l.lru.Get(i); ok {
			oe := v.(*clogEntry)
			if e.match(oe) {
				// Don't update change time if nothing has changed.
				// This is strictly for refresh decision.
				e.changed = oe.changed
			}
		}
	}
	l.lru.Add(i, e)
}

func parent(name upspin.PathName) upspin.PathName {
	pp := string(name)
	i := strings.LastIndex(pp, "/")
	if i < 0 {
		return ""
	}
	return upspin.PathName(pp[:i])
}

// removeFromGlob removes an entry from a glob, should that glob exist.
func (l *clog) removeFromGlob(e *clogEntry) {
	dirName := parent(e.name)
	if dirName == "" {
		return
	}
	i := lruKey{name: dirName, ep: *e.ep, glob: true}
	if v, ok := l.lru.Get(i); ok {
		ge := v.(*clogEntry)
		if _, ok := ge.children[e.name]; ok {
			delete(ge.children, e.name)
		}
	}
}

// addToGlob creates the glob if it doesn't exist and adds an entry to it.
func (l *clog) addToGlob(e *clogEntry) {
	dirName := parent(e.name)
	if dirName == "" {
		return
	}
	i := lruKey{name: dirName, ep: *e.ep, glob: true}
	v, ok := l.lru.Get(i)
	var ge *clogEntry
	if !ok {
		ge = &clogEntry{
			request:  globReq,
			name:     dirName,
			ep:       e.ep,
			children: make(map[upspin.PathName]struct{}),
		}
		l.lru.Add(i, ge)
	} else {
		ge = v.(*clogEntry)
	}
	ge.children[e.name] = struct{}{}
}

// appendToLogFile appends to the clog file.
func (l *clog) appendToLogFile(e *clogEntry) error {
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	// Wrap with a count.
	buf = appendBytes(nil, buf)

	if _, err = l.file.Write(buf); err != nil {
		return err
	}
	return nil
}

// reutrns true if the error is from the
func cacheable(err error) bool {
	if err == nil {
		return true
	}
	if e, ok := err.(*errors.Error); ok {
		switch e.Kind {
		case errors.NotExist:
			return true
		default:
			return false
		}
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
		failed := make(map[upspin.Endpoint]struct{})

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

// refreshLoop iterates through the LRU refreshing as it goes. It terminates either when it reaches
// the end of the LRU, has refreshed maxRefreshPerRound entries, or is told to die.
func (l *clog) refreshLoop(iter *cache.Iterator, failed map[upspin.Endpoint]struct{}, globOnly bool) *cache.Iterator {
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
		if _, ok := failed[*e.ep]; ok {
			continue
		}

		if (globOnly && e.request != globReq) || (!globOnly && e.request == globReq) {
			continue
		}

		l.Lock()
		sinceChanged := time.Since(e.changed)
		sinceRefreshed := time.Since(e.refreshed)
		l.Unlock()
		if time.Duration(2.0)*sinceRefreshed > sinceChanged || sinceRefreshed > time.Hour {
			if l.refresh(e) {
				log.Debug.Printf("dircacheserver: refresh %s OK\n", e.name)
				n++
			} else {
				log.Debug.Printf("dircacheserver: refresh %s failed\n", e.name)
				failed[*e.ep] = struct{}{}
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
	l.Lock()
	defer l.Unlock()
	switch e.request {
	case globReq:
		entries, err := dir.Glob(string(e.name))
		if !cacheable(err) {
			return false
		}
		l.logGlobRequest(e.ep, e.name, err, entries)
	default:
		de, err := dir.Lookup(e.name)
		if !cacheable(err) {
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

// match matches two log entries.
func (e *clogEntry) match(oe *clogEntry) bool {
	return match(e, oe.de, oe.error)
}

func (l *clog) matchGlob(e *clogEntry, entries []*upspin.DirEntry, err error) bool {
	if len(e.children) != len(entries) {
		return false
	}
	if !matchErrors(e.error, err) {
		return false
	}
	for _, de := range entries {
		found := false
		if _, ok := e.children[de.Name]; !ok {
			return false
		}
		v, ok := l.lru.Get(lruKey{name: de.Name, ep: *e.ep, glob: false})
		if !ok {
			return false
		}
		ee := v.(*clogEntry)
		if ee.de != nil && ee.de.Sequence == de.Sequence {
			found = true
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
	lookupReq:     "lookup",
	globReq:       "glob",
	deleteReq:     "delete",
	putReq:        "put",
	deleteReqDone: "deleteDone",
	putDoneReq:    "putDone",
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
		rv += fmt.Sprintf(" de<%v>", *e.de)
	}
	if e.children != nil {
		rv += fmt.Sprintf(" children<%v>", e.children)
	}
	if e.complete {
		rv += " complete"
	}
	return rv
}
