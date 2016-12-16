// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

// This file defines and implements a replayable log for the directory cache.
//
// Cache entries are kept a fixed size LRU. Therefore, we do not maintain a
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
// consistency. This consistency is implemented by the refresh goroutine which will
// periodically refresh all entries. The refresh interval increases if the entry is
// unchanged, reflecting file inertia.
//
// We store in individual globReq entries, the pertinent Access file, if any. This is
// updated as we learn more about Access files through Glob, Put, Lookup, Delete or
// WhichAccess. Since we maintain an LRU of known DirEntries rather than a tree, we
// must run the LRU whenever an Access file is added or removed to flush any stale
// entries.

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
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
	versionReq
	maxReq

	version = "4" // Must increment every time we change log file format.
)

// noAccessFile is used to indicate we did a WhichAccess and it returned no DirEntry.
const noAccessFile = upspin.PathName("no known Access file")

// clogEntry corresponds to an cached operation.
type clogEntry struct {
	request request
	name    upspin.PathName

	// The error returned on a request.
	error error

	// de is the directory entry returned by the RPC.
	de *upspin.DirEntry

	// The contents of a directory.
	children map[string]bool
	complete bool // true if the children are the complete set

	// For directories, the Access file that pertains.
	access upspin.PathName

	// The times are used to determine when to refresh a cached entry.
	changed   time.Time // when entry was set or changed
	refreshed time.Time // when entry was last refreshed
}

// clog represents the replayable log of DirEntry changes.
type clog struct {
	ctx           upspin.Context
	dir           string        // directory clog lives in
	refreshPeriod time.Duration // Duration between refreshes
	maxDisk       int64         // most bytes taken by on disk logs
	lru           *cache.LRU    // [lruKey]*clogEntry

	userToDirServer *userToDirServer // map from user name to DirServer endpoint

	exit            chan bool // closing signals child routines to exit
	refresherExited chan bool // closing confirms the refresher is exiting
	rotate          chan bool // input signals the rotater to rotate the logs
	rotaterExited   chan bool // closing confirms the rotater is exiting

	// globalLock keeps everyone else out when we are traversing the whole LRU to
	// update Access files.
	globalLock sync.RWMutex

	// logFileLock provides exclusive access to the log file.
	logFileLock sync.Mutex
	file        *os.File
	wr          *bufio.Writer
	logSize     int64
	order       int64

	pathLocks hashLockArena
	globLocks hashLockArena
}

// hashLockArena is an arena of hashed locks.
type hashLockArena struct {
	hashLock [255]sync.Mutex
}

// lruKey is the lru key. globs are distinguished from other entries because
// the pattern could clash with a name.
type lruKey struct {
	name upspin.PathName
	glob bool
}

// LRUMax is the maximum number of entries in the LRU.
const LRUMax = 10000

// oldestFirst and newestFirst are used to sort a directory.
type oldestFirst []os.FileInfo

func (a oldestFirst) Len() int           { return len(a) }
func (a oldestFirst) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a oldestFirst) Less(i, j int) bool { return a[i].ModTime().Before(a[j].ModTime()) }

type newestFirst []os.FileInfo

func (a newestFirst) Len() int           { return len(a) }
func (a newestFirst) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a newestFirst) Less(i, j int) bool { return a[j].ModTime().Before(a[i].ModTime()) }

// openLog reads the current log.
// - dir is the directory for log files.
// - maxDisk is an approximate limit on disk space for log files
// - userToDirServer is a map from user names to directory endpoints, maintained by the server
func openLog(ctx upspin.Context, dir string, maxDisk int64, userToDirServer *userToDirServer) (*clog, error) {
	const op = "grpc/dircacheserver.openLog"
	log.Debug.Printf(op)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	if userToDirServer == nil {
		userToDirServer = newUserToDirServer()
	}
	l := &clog{
		ctx:             ctx,
		dir:             dir,
		lru:             cache.NewLRU(LRUMax),
		refreshPeriod:   30 * time.Second,
		maxDisk:         maxDisk,
		exit:            make(chan bool),
		refresherExited: make(chan bool),
		rotate:          make(chan bool),
		rotaterExited:   make(chan bool),
		userToDirServer: userToDirServer,
	}

	// updateLRU expect these to be held.
	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	// Read the log files, oldest first. Remember highest Order encountered
	// before an error.
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	infos, err := f.Readdir(0)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Sort(oldestFirst(infos))
	highest := int64(-1)
	for i := range infos {
		err := l.readLogFile(l.dir + "/" + infos[i].Name())
		if err != nil && highest == -1 {
			highest = l.order
		}
	}
	l.order = highest

	// Start a new log.
	l.rotateLog()

	go l.refresher()
	go l.rotater()
	return l, nil
}

// rotateLog creates a new log file and removes enough old ones to stay under
// the l.maxDisk limit.
func (l *clog) rotateLog() {
	const op = "grpc/dircacheserver.readLogFile"
	// Flush current file.
	l.logFileLock.Lock()
	if l.wr != nil {
		l.wr.Flush()
	}
	l.logFileLock.Unlock()

	// Trim the logs.
	f, err := os.Open(l.dir)
	if err != nil {
		log.Info.Printf("%s: %", op, err)
		return
	}
	infos, err := f.Readdir(0)
	f.Close()
	if err != nil {
		log.Info.Printf("%s: %", op, err)
		return
	}
	sort.Sort(newestFirst(infos))
	var len int64
	for i := range infos {
		len += infos[i].Size()
		if len > 3*l.maxDisk/4 {
			os.Remove(l.dir + "/" + infos[i].Name())
		}
	}

	// Create a new log file and make it current.
	f, err = ioutil.TempFile(l.dir, "clog")
	if err != nil {
		log.Info.Printf("%s: %", op, err)
		return
	}
	l.logFileLock.Lock()
	if l.file != nil {
		l.wr.Flush()
		l.file.Close()
	}
	l.file = f
	l.wr = bufio.NewWriter(f)
	l.logSize = 0
	l.logFileLock.Unlock()

	l.appendToLogFile(&clogEntry{request: versionReq, name: version})
}

// rotater is a goroutine that is woken whenever we need to trim the
// logs to stay below the maxDisk limit.
func (l *clog) rotater() {
	for {
		select {
		case <-l.exit:
			close(l.rotaterExited)
			return
		case <-l.rotate:
		}
		l.rotateLog()
	}
}

// readLogFile reads a single log file. The log file must begin and end with a version record.
func (l *clog) readLogFile(fn string) error {
	const op = "grpc/dircacheserver.readLogFile"

	// Open the log file.  If one didn't exist, just rename the new log file and return.
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer f.Close()

	rd := bufio.NewReader(f)

	// First request must be the right version.
	var e clogEntry
	if err := e.read(rd); err != nil {
		log.Info.Printf("%s: %s", op, err)
		return err
	}
	if e.request != versionReq {
		log.Info.Printf("%s: log %s: first entry not version request", op, fn)
		return badVersion
	} else if e.name != version {
		log.Info.Printf("%s: log %s: expected version %s got %s", op, fn, version, e.name)
		return badVersion
	}
	for {
		var e clogEntry
		if err := e.read(rd); err != nil {
			if err == io.EOF {
				break
			}
			log.Info.Printf("%s: %s", op, err)
			break
		}
		switch e.request {
		case versionReq:
			log.Info.Printf("%s: verson other than first record", op)
			break
		case globReq:
			// Since we first log all the contents of a directory before the glob,
			// we need to first add all entries to a manufactured glob entry. Once
			// we read the actual glob entry, we need to find this manufactured
			// glob and mark it complete. If the directory was empty, that glob
			// will not yet exist and we must manufacture it also.
			v, ok := l.lru.Get(lruKey{name: e.name, glob: true})
			if ok {
				e.children = v.(*clogEntry).children
				e.complete = true
			} else {
				e.children = make(map[string]bool)
			}
			l.updateLRU(&e)
		default:
			l.updateLRU(&e)
		}
	}
	return nil
}

func (l *clog) myDirServer(pathName upspin.PathName) bool {
	name := string(pathName)
	// Pull off the user name.
	var userName string
	slash := strings.IndexByte(name, '/')
	if slash < 0 {
		userName = name
	} else {
		userName = name[:slash]
	}
	return userName == string(l.ctx.UserName())
}

func (l *clog) close() error {
	// Write out partials.
	if l.wr != nil {
		l.wr.Flush()
	}

	// Stop go routines.
	close(l.exit)
	<-l.refresherExited
	<-l.rotaterExited

	return l.file.Close()
}

func (l *clog) lookup(name upspin.PathName) (*upspin.DirEntry, error, bool) {
	if *memprofile != "" && string(name) == string(l.ctx.UserName())+"/"+"memstats" {
		dumpMemStats()
	}

	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	plock := l.pathLocks.lock(name)
	v, ok := l.lru.Get(lruKey{name: name, glob: false})
	plock.Unlock()
	if ok {
		e := v.(*clogEntry)
		return e.de, e.error, true
	}

	// Look for a complete globReq. If there is one and it doesn't list
	// this name, we can return a NotExist error.
	dirName := path.DropPath(name, 1)
	glock := l.globLocks.lock(dirName)
	defer glock.Unlock()
	v, ok = l.lru.Get(lruKey{name: dirName, glob: true})
	if !ok {
		return nil, nil, false
	}
	e := v.(*clogEntry)
	if !e.complete {
		return nil, nil, false
	}
	if e.children[lastElem(name)] {
		// The glob entry contains this but we dropped the actual entry.
		return nil, nil, false
	}
	// Craft an error and return it.
	return nil, errors.E(errors.NotExist, errors.Errorf("%s does not exist", name)), true
}

func (l *clog) lookupGlob(pattern upspin.PathName) ([]*upspin.DirEntry, error, bool) {
	dirPath, ok := cacheableGlob(pattern)
	if !ok {
		return nil, nil, false
	}

	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	// Lookup the glob.
	glock := l.globLocks.lock(dirPath)
	defer glock.Unlock()
	v, ok := l.lru.Get(lruKey{name: dirPath, glob: true})
	if !ok {
		return nil, nil, false
	}
	e := v.(*clogEntry)
	if !e.complete {
		return nil, nil, false
	}
	// Lookup all the individual entries.  If any are missing, no go.
	var entries []*upspin.DirEntry
	for n := range e.children {
		name := path.Join(e.name, n)
		plock := l.pathLocks.lock(name)
		v, ok := l.lru.Get(lruKey{name: name, glob: false})
		if !ok {
			e.complete = false
			plock.Unlock()
			return nil, nil, false
		}
		ce := v.(*clogEntry)
		if ce.error != nil || ce.de == nil {
			e.complete = false
			plock.Unlock()
			return nil, nil, false
		}
		entries = append(entries, ce.de)
		plock.Unlock()
	}
	return entries, e.error, ok
}

func (l *clog) whichAccess(name upspin.PathName) (*upspin.DirEntry, bool) {
	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	// Get name of access file.
	dirName := path.DropPath(name, 1)
	glock := l.globLocks.lock(dirName)
	defer glock.Unlock()
	v, ok := l.lru.Get(lruKey{name: dirName, glob: true})
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
	plock := l.pathLocks.lock(e.access)
	defer plock.Unlock()
	v, ok = l.lru.Get(lruKey{name: e.access})
	if !ok {
		return nil, false
	}
	e = v.(*clogEntry)
	return e.de, true
}

func (l *clog) logRequest(op request, name upspin.PathName, err error, de *upspin.DirEntry) {
	if !l.myDirServer(name) {
		return
	}
	if !cacheableError(err) {
		return
	}

	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	e := &clogEntry{
		name:    name,
		request: op,
		error:   err,
		de:      de,
	}
	l.append(e)

	// Optimization: when creating a directory, fake a complete globReq entry since
	// we know that the directory is empty and don't have to ask the server.
	if op == putReq && err == nil && de != nil && de.IsDir() {
		e := &clogEntry{
			name:     name,
			request:  globReq,
			error:    err,
			children: make(map[string]bool),
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

func (l *clog) logGlobRequest(pattern upspin.PathName, err error, entries []*upspin.DirEntry) {
	if !l.myDirServer(pattern) {
		return
	}
	if !cacheableError(err) {
		return
	}
	dirName, ok := cacheableGlob(pattern)
	if !ok {
		return
	}

	l.globalLock.RLock()
	defer l.globalLock.RUnlock()

	// Log each entry.
	children := make(map[string]bool)
	for _, de := range entries {
		children[lastElem(de.Name)] = true
		l.logRequest(lookupReq, de.Name, err, de)
	}

	// If any files have disappeared from a preexisting glob, remove them.
	glock := l.globLocks.lock(dirName)
	v, ok := l.lru.Get(lruKey{name: dirName, glob: true})
	var todelete []*clogEntry
	if ok {
		oe := v.(*clogEntry)
		for n := range oe.children {
			if !children[n] {
				e := &clogEntry{request: deleteReq, name: path.Join(dirName, n)}
				todelete = append(todelete, e)
			}
		}
	}
	glock.Unlock()

	// The deleted files may have been recreated while we were doing this. Just forget
	// what we know about them.
	for _, e := range todelete {
		l.removeFromLRU(e, true)
		l.removeFromLRU(e, false)
	}

	// Log the glob itself.
	e := &clogEntry{
		request:  globReq,
		name:     dirName,
		error:    err,
		children: children,
		complete: true,
	}
	l.append(e)
}

// append appends a clogEntry to the end of the clog and replaces existing in the LRU.
func (l *clog) append(e *clogEntry) error {
	const op = "grpc/dircacheserver.append"

	l.updateLRU(e)
	l.appendToLogFile(e)

	return nil
}

// updateLRU adds the entry to the in core LRU version of the clog. We don't remember errors
// in the LRU (other than ErrFollowLink). However, we do use them to remove things from the LRU.
//
// updateLRU returns non zero if the state was changed other than updated refresh times.
func (l *clog) updateLRU(e *clogEntry) (changes int) {
	if e.error != nil {
		// Remember links.  All cases are equivalent, i.e., treat them like a lookup.
		if e.error == upspin.ErrFollowLink {
			e.request = lookupReq
			changes += l.addToLRU(e)
			changes += l.addToGlob(e)
			return
		}
		if !errors.Match(notExist, e.error) {
			log.Debug.Printf("updateLRU %s error %s", e.name, e.error)
			return
		}
		// Recursively remove from everywhere possible.
		if e.request == globReq {
			for k := range e.children {
				ae := &clogEntry{
					request: globReq,
					name:    path.Join(e.name, k),
					error:   e.error,
					de:      e.de,
				}
				changes += l.updateLRU(ae)
			}
		}
		changes += l.removeFromLRU(e, true)
		changes += l.removeFromLRU(e, false)
		changes += l.removeFromGlob(e)
		changes += l.removeAccess(e)

		// Add back in as a non-existent file.
		e.request = lookupReq
		changes += l.addToLRU(e)
		return
	}

	// At this point, only requests have gotten through.
	switch e.request {
	case deleteReq:
		changes += l.removeFromLRU(e, true)
		changes += l.removeFromLRU(e, false)
		changes += l.removeFromGlob(e)
		changes += l.removeAccess(e)
	case putReq:
		changes += l.addToLRU(e)
		changes += l.addToGlob(e)
		changes += l.addAccess(e)
	case lookupReq, globReq:
		changes += l.addToLRU(e)
		changes += l.addToGlob(e)
		changes += l.addAccess(e)
	case whichAccessReq:
		// Log the access file itself as a lookup.
		if e.de != nil {
			ae := &clogEntry{
				request: lookupReq,
				name:    e.de.Name,
				error:   nil,
				de:      e.de,
			}
			changes += l.addAccess(ae)
		}

		// Add it to the specific entry.
		dirName := path.DropPath(e.name, 1)
		glock := l.globLocks.lock(dirName)
		defer glock.Unlock()
		v, ok := l.lru.Get(lruKey{name: dirName, glob: true})
		if ok {
			newVal := noAccessFile
			if e.de != nil {
				newVal = e.de.Name
			}
			if v.(*clogEntry).access != newVal {
				changes++
				v.(*clogEntry).access = newVal
			}
		}
	default:
		log.Printf("unknown request type: %s", e)
	}
	return
}

// addToLRU adds an entry to the LRU updating times used for refresh.
// Return non zero if state other than the refresh time has changed.
func (l *clog) addToLRU(e *clogEntry) (changes int) {
	changes = 1
	e.refreshed = time.Now()
	e.changed = e.refreshed
	k := lruKey{name: e.name, glob: e.request == globReq}
	var lock *sync.Mutex
	if e.request == globReq {
		lock = l.globLocks.lock(e.name)
	} else {
		lock = l.pathLocks.lock(e.name)
	}
	if v, ok := l.lru.Get(k); ok {
		oe := v.(*clogEntry)
		if match(e, oe) {
			// Don't update change time if nothing has changed.
			// This is strictly for refresh decision.
			e.changed = oe.changed
			changes = 0
		}
	}
	l.lru.Add(k, e)
	lock.Unlock()
	return changes
}

// removeFromLRU removes an entry from the LRU.
// Return non zero if state other than the refresh time has changed.
func (l *clog) removeFromLRU(e *clogEntry, isGlob bool) int {
	v := l.lru.Remove(lruKey{name: e.name, glob: isGlob})
	if v != nil {
		return 1
	}
	return 0
}

// addToGlob creates the glob if it doesn't exist and adds an entry to it.
func (l *clog) addToGlob(e *clogEntry) (changes int) {
	dirName := path.DropPath(e.name, 1)
	if dirName == e.name {
		return
	}
	var ge *clogEntry
	k := lruKey{name: dirName, glob: true}
	glock := l.globLocks.lock(dirName)
	defer glock.Unlock()
	if v, ok := l.lru.Get(k); !ok {
		now := time.Now()
		ge = &clogEntry{
			request:   globReq,
			name:      dirName,
			children:  make(map[string]bool),
			complete:  false,
			changed:   now,
			refreshed: now,
		}
		l.lru.Add(k, ge)
	} else {
		ge = v.(*clogEntry)
	}
	lelem := lastElem(e.name)
	if !ge.children[lelem] {
		changes = 1
		ge.children[lelem] = true
	}
	return
}

// removeFromGlob removes an entry from a glob, should that glob exist.
func (l *clog) removeFromGlob(e *clogEntry) (changes int) {
	dirName := path.DropPath(e.name, 1)
	if dirName == e.name {
		return
	}
	lelem := lastElem(e.name)
	k := lruKey{name: dirName, glob: true}
	glock := l.globLocks.lock(dirName)
	if v, ok := l.lru.Get(k); ok {
		ge := v.(*clogEntry)
		if ge.children[lelem] {
			changes = 1
			delete(ge.children, lelem)
		}
	}
	glock.Unlock()
	return
}

// addAccess adds an access pointer to its directory and removes one
// from all descendant directories that point to an ascendant of the
// access file's directory.
//
// Since this walks through many entries in the LRU, it grabs the
// global write lock to keep every other thread out.
func (l *clog) addAccess(e *clogEntry) (changes int) {
	if !access.IsAccessFile(e.name) {
		return
	}
	l.globalLock.RUnlock()
	l.globalLock.Lock()

	// Add the access reference to its immediate directory.
	dirName := path.DropPath(e.name, 1)
	v, ok := l.lru.Get(lruKey{name: dirName, glob: true})
	if ok {
		if v.(*clogEntry).access != e.name {
			changes++
			v.(*clogEntry).access = e.name
		}
	}

	// Remove the access reference for any descendant that points at an ascendant.
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
			// This is different than noAccessFile because the
			// empty string means that we don't know.
			if ne.access != "" {
				changes++
				ne.access = ""
			}
		}
	}
	l.globalLock.Unlock()
	l.globalLock.RLock()
	return
}

// removeAccess removes an access pointer from its directory and
// from any descendant directory. Since it needs to run the LRU
// it must lock out everyone else while it is doing it.
//
// removeAccess assumes that it was entered with globalLock.RLock held
// and that it must upgrade that to globalLock.Lock to do its work.
func (l *clog) removeAccess(e *clogEntry) (changes int) {
	if !access.IsAccessFile(e.name) {
		return
	}
	l.globalLock.RUnlock()
	l.globalLock.Lock()

	// Remove this access reference from its immediate directory.
	dirName := path.DropPath(e.name, 1)
	v, ok := l.lru.Get(lruKey{name: dirName, glob: true})
	if ok {
		v.(*clogEntry).access = ""
		changes++
	}

	// Remove this access reference from any descendant.
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
			changes++
			ne.access = ""
		}
	}
	l.globalLock.Unlock()
	l.globalLock.RLock()
	return
}

// appendToLogFile appends to the clog file.
func (l *clog) appendToLogFile(e *clogEntry) error {
	buf, err := e.marshal()
	if buf == nil {
		// Either an error or nothing to marshal.
		return err
	}

	// Wrap with a count.
	buf = appendBytes(nil, buf)

	l.logFileLock.Lock()
	defer l.logFileLock.Unlock()
	if l.file == nil {
		return nil
	}
	n, err := l.wr.Write(buf)
	l.logSize += int64(n)
	if l.logSize > l.maxDisk/8 || err != nil {
		// Don't block waking the goroutine,
		select {
		case l.rotate <- true:
		default:
		}
	}
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
var badVersion = errors.E(errors.Invalid, errors.Errorf("bad log file version"))

// A marshalled entry is of the form:
//   request-type: byte
//   error: len + marshalled upspin.Error
//   direntry: len + marshalled upspin.DirEntry
//   if direntry == nil {
//     name: string
//   }
//   if request-type == reqGlob {
//     number-of-children: varint
//     children: strings containing the last element of the child name.
//   }
//
// Strings, directory entries, and errors are preceded by a
// Varint byte count.

// marshal packs the clogEntry into a new byte slice for storage.
func (e *clogEntry) marshal() ([]byte, error) {
	if e.request >= maxReq {
		return nil, errors.Errorf("unknown clog operation %d", e.request)
	}
	if e.request == globReq && !e.complete {
		return nil, nil
	}
	b := []byte{byte(e.request)}
	b = appendError(b, e.error)
	var err error
	b, err = appendDirEntry(b, e.de)
	if err != nil {
		return nil, err
	}
	if e.de == nil {
		b = appendString(b, string(e.name))
	}
	if e.request == globReq {
		b = appendChildren(b, e.children)
	}
	return b, nil
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
	b = b[1:]
	if e.error, b, err = getError(b); err != nil {
		return err
	}
	if e.de, b, err = getDirEntry(b); err != nil {
		return err
	}
	if e.de == nil {
		var str string
		if str, b, err = getString(b); err != nil {
			return err
		}
		e.name = upspin.PathName(str)
	} else {
		e.name = e.de.Name
	}
	if e.request == globReq {
		if e.children, b, err = getChildren(b); err != nil {
			return err
		}
		e.complete = true
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
		return nil, b, tooShort
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

func appendChildren(b []byte, children map[string]bool) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(children)))
	b = append(b, tmp[:n]...)
	for k := range children {
		b = appendString(b, k)
	}
	return b
}

func getChildren(b []byte) (children map[string]bool, remaining []byte, err error) {
	u, n := binary.Varint(b)
	if n == 0 {
		return nil, b, tooShort
	}
	remaining = b[n:]
	children = make(map[string]bool)
	for i := 0; i < int(u); i++ {
		var s string
		s, remaining, err = getString(remaining)
		if err != nil {
			return
		}
		children[s] = true
	}
	return
}

const maxRefreshPerRound = 100 // The max number of refreshes we will perform per refresh round

// refresher is a goroutine that refreshes entries in the LRU.  It does this in
// bursts to make life easier on connections that have a long setup time, such as
// cellular links.
//
// The longer an entry has gone since changing, the longer the refresh period. The
// assumption is that the longer since something has changed, the longer it will be
// till it is next changed.
//
// TODO(p): This is far from an optimal refresh policy. We could use the Merkle structure
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
		ep := l.userToDirServer.Get(e.name)
		if ep == nil || failed[*ep] {
			continue
		}

		if (globOnly && e.request != globReq) || (!globOnly && e.request == globReq) {
			continue
		}

		var lock *sync.Mutex
		if e.request == globReq {
			lock = l.globLocks.lock(e.name)
		} else {
			lock = l.pathLocks.lock(e.name)
		}
		sinceChanged := time.Since(e.changed)
		sinceRefreshed := time.Since(e.refreshed)
		lock.Unlock()
		if 2*sinceRefreshed > sinceChanged || sinceRefreshed > maxRefreshPeriod {
			if l.refresh(e, ep) {
				log.Debug.Printf("dircacheserver: refresh %s OK\n", e.name)
				n++
			} else {
				log.Debug.Printf("dircacheserver: refresh %s failed\n", e.name)
				failed[*ep] = true
			}
		}
	}
	return iter
}

// refresh refreshes a single entry. Returns true if the refresh happened.
func (l *clog) refresh(e *clogEntry, ep *upspin.Endpoint) bool {
	dir, err := bind.DirServer(l.ctx, *ep)
	if err != nil {
		// plumbing problem
		return false
	}
	switch e.request {
	case globReq:
		pattern := path.Join(e.name, "/*")
		entries, err := dir.Glob(string(pattern))
		if !cacheableError(err) {
			return false
		}
		l.logGlobRequest(pattern, err, entries)
	default:
		de, err := dir.Lookup(e.name)
		if !cacheableError(err) {
			return false
		}
		l.logRequest(lookupReq, e.name, err, de)
	}
	return true
}

// match matches a log entry to the return from a lookup.
func match(e, oe *clogEntry) bool {
	if !matchErrors(e.error, oe.error) {
		return false
	}
	if e.request != globReq {
		if e.de == nil && oe.de == nil {
			return true
		}

		if e.de == nil || oe.de == nil {
			return false
		}
		return e.de.Sequence == oe.de.Sequence
	}
	if len(e.children) != len(oe.children) {
		return false
	}
	for c := range oe.children {
		if !e.children[c] {
			return false
		}
	}
	return true
}

func matchErrors(e1, e2 error) bool {
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
	versionReq:     "version",
}

func (e *clogEntry) String() string {
	rv := "?"
	if e.request >= lookupReq && e.request < maxReq {
		rv = reqName[e.request]
	}
	rv += fmt.Sprintf(" %s ", e.name)
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

func lastElem(path upspin.PathName) string {
	str := string(path)
	lastSlash := strings.LastIndexByte(str, '/')
	if lastSlash < 0 {
		return ""
	}
	return str[lastSlash+1:]
}

var memprofile = flag.String("memprofile", "", "write memory profile to `file`")

func dumpMemStats() {
	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatalf("could not create memory profile: %s", err)
		}
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatalf("could not write memory profile: %s", err)
		}
		f.Close()
	}
}

func (a *hashLockArena) lock(name upspin.PathName) *sync.Mutex {
	var hash uint32
	for _, i := range []byte(name) {
		hash = hash*7 + uint32(i)
	}
	lock := &a.hashLock[hash%uint32(len(a.hashLock))]
	lock.Lock()
	return lock
}
