// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircache // import "upspin.io/dir/dircache"

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

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"

	"upspin.io/access"
	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// request is the requested operation to be performed on the DirEntry.
type request int

const (
	// Requests that correspond to DirServer Calls.
	lookupReq request = iota
	globReq
	deleteReq
	putReq
	whichAccessReq

	// versionReq is the first request in each log file. If
	// the version doesn't match the binary, we ignore the
	// file.
	versionReq

	// obsoleteReq marks an LRU entry as no longer necessarily
	// matching what is in the directory server. Whenever the
	// watcher gets an error saying that the log cannot be
	// watched at its current sequence, we mark all entries obsolete.
	//
	// obsoleteReq entries are never written to log files.
	obsoleteReq

	// maxReq is one past the highest legal value for a request.
	maxReq

	// version is the version of the formatting of the log file.
	// It must change every time we change the log file format.
	version = "20170922"
)

// clogEntry corresponds to a cached operation.
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

	// True if we know a directory has no access file.
	noAccessFile bool

	// The watch sequence number.
	sequence int64
}

// clog represents the replayable log of DirEntry changes.
type clog struct {
	cfg     upspin.Config
	dir     string     // directory clog lives in
	maxDisk int64      // most bytes taken by on disk logs
	lru     *cache.LRU // [lruKey]*clogEntry

	proxied *proxiedDirs // the servers being proxied

	exit          chan bool // closing signals child routines to exit
	rotate        chan bool // input signals the rotater to rotate the logs
	rotaterExited chan bool // closing confirms the rotater is exiting

	// globalLock serializes requests.
	globalLock sync.RWMutex

	// logFileLock provides exclusive access to the log file.
	logFileLock    sync.Mutex
	file           *os.File
	wr             *bufio.Writer
	logSize        int64 // current log file size in bytes
	highestLogFile int   // highest numbered logfile

	// sequenceLRU contains the highest sequence number seen
	// for a path. Watch will ignore any events with an equal or
	// lower sequence.
	sequenceLRU *cache.LRU // [PathName]int64

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

const (
	// LRUMax is the maximum number of entries in the LRU.
	LRUMax = 10000

	// SequenceLRUMax is the maximum number of paths that
	// whose sequence we will remember. This is used to
	// avoid reapplying Watch Events that reflect actions
	// we have already applied or overridden.
	SequenceLRUMax = 2000
)

// openLog reads the current log.
// - dir is the directory for log files.
// - maxDisk is an approximate limit on disk space for log files
// - userToDirServer is a map from user names to directory endpoints, maintained by the server
func openLog(cfg upspin.Config, dir string, maxDisk int64) (*clog, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	l := &clog{
		cfg:           cfg,
		dir:           dir,
		lru:           cache.NewLRU(LRUMax),
		maxDisk:       maxDisk,
		exit:          make(chan bool),
		rotate:        make(chan bool),
		rotaterExited: make(chan bool),
		sequenceLRU:   cache.NewLRU(SequenceLRUMax),
	}
	l.proxied = newProxiedDirs(l)

	// Read the log files in ascending time order.
	files, highestLogFile, err := listSorted(dir, true)
	if err != nil {
		return nil, err
	}
	l.highestLogFile = highestLogFile
	for _, lfi := range files {
		l.readLogFile(lfi.Name(l.dir))
	}

	// Start a new log.
	l.rotateLog()

	go l.rotater()
	return l, nil
}

func (l *clog) cacheable(path upspin.PathName, ep *upspin.Endpoint) bool {
	return l.proxied.cacheable(path, ep)
}

func (l *clog) retryWatch(parsed path.Parsed) {
	l.proxied.retryWatch(parsed)
}

// rotateLog creates a new log file and removes enough old ones to stay under
// the l.maxDisk limit.
func (l *clog) rotateLog() {
	const op errors.Op = "rpc/dircache.rotateLog"

	l.flush()

	// Trim the logs.
	files, _, err := listSorted(l.dir, false)
	if err != nil {
		return
	}
	var len int64
	for _, lfi := range files {
		len += lfi.Size()
		if len > 3*l.maxDisk/4 {
			fn := lfi.Name(l.dir)
			log.Debug.Printf("%s: remove log file %s", op, fn)
			if err := os.Remove(fn); err != nil {
				log.Info.Printf("%s: %s", op, err)
			}
		}
	}

	// Create a new log file and make it current.
	l.highestLogFile++
	lfi := &logFileInfo{number: l.highestLogFile}
	f, err := os.OpenFile(lfi.Name(l.dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0700)
	if err != nil {
		log.Info.Printf("%s: %s", op, err)
		return
	}
	log.Debug.Printf("%s: new log file %s", op, f.Name())
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

// wipeLog is called whenever we suspect the cache to not reflect
// the true contents of the directory.
//
// Make LRU entries obsolete so that we don't believe them. However, we
// leave them in the LRU since they do tell us what files the users
// were most recently interested in. The watcher should eventually
// replace them with trusted information.
func (l *clog) wipeLog(user upspin.UserName) {
	log.Info.Printf("wiping log")

	l.globalLock.Lock()
	defer l.globalLock.Unlock()

	// Obsolete all entries for this user.
	iter := l.lru.NewIterator()
	for {
		_, v, ok := iter.GetAndAdvance()
		if !ok {
			break
		}
		e := v.(*clogEntry)

		parsed, err := path.Parse(e.name)
		if err != nil || parsed.User() != user {
			continue
		}

		// Glob requests can be marked as incomplete.
		if e.request == globReq || e.request == obsoleteReq {
			e.complete = false
			continue
		}
		e.request = obsoleteReq
	}

	// Forget any sequence numbers since they may no longer make sense.
	l.sequenceLRU = cache.NewLRU(SequenceLRUMax)
}

func (l *clog) flush() {
	// Flush current file.
	l.logFileLock.Lock()
	if l.wr != nil {
		l.wr.Flush()
	}
	l.logFileLock.Unlock()
}

type logFileInfo struct {
	number int
	size   int64
}
type ascendingOrder []*logFileInfo

func (a ascendingOrder) Len() int           { return len(a) }
func (a ascendingOrder) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ascendingOrder) Less(i, j int) bool { return a[i].number < a[j].number }

func (lfi logFileInfo) Size() int64            { return lfi.size }
func (lfi logFileInfo) Name(dir string) string { return fmt.Sprintf("%s/clog.%08d", dir, lfi.number) }

// listSorted returns a list of log files in ascending or descending order.
// It also returns the number of the highest found, or zero if none found.
// Files not matching the log name pattern are removed.
func listSorted(dir string, ascending bool) ([]*logFileInfo, int, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, 0, err
	}
	infos, err := f.Readdir(0)
	f.Close()
	if err != nil {
		return nil, 0, err
	}
	var lfis []*logFileInfo
	highest := 0
	for i := range infos {
		fi := parseLogName(infos[i])
		if fi == nil {
			// If it doesn't parse, remove it.
			if err := os.Remove(infos[i].Name()); err != nil {
				log.Info.Printf("rpc/dircache.listSorted: %s", err)
			}
			continue
		}
		lfis = append(lfis, fi)
		if fi.number > highest {
			highest = fi.number
		}
	}
	if lfis == nil {
		return nil, 0, nil
	}
	sort.Sort(ascendingOrder(lfis))
	if !ascending {
		for i, j := 0, len(lfis)-1; i < j; i, j = i+1, j-1 {
			lfis[i], lfis[j] = lfis[j], lfis[i]
		}
	}

	return lfis, highest, nil
}

func parseLogName(fi os.FileInfo) *logFileInfo {
	parts := strings.Split(fi.Name(), ".")
	if len(parts) != 2 || parts[0] != "clog" {
		return nil
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	return &logFileInfo{size: fi.Size(), number: n}
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
	const op errors.Op = "rpc/dircache.readLogFile"

	log.Debug.Printf("%s: %s", op, fn)

	// Open the log file.  If one didn't exist, just rename the new log file and return.
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer f.Close()

	rd := bufio.NewReader(f)

	// First request must be the right version.
	var e clogEntry
	if err := e.read(l, rd); err != nil {
		if err != io.EOF {
			log.Info.Printf("%s: %s", op, err)
		}
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
		if err := e.read(l, rd); err != nil {
			if err == io.EOF {
				break
			}
			log.Info.Printf("%s: %s", op, err)
			break
		}
		switch e.request {
		case versionReq:
			log.Info.Printf("%s: version other than first record", op)
		case globReq:
			// Since we first log all the contents of a directory before the glob,
			// we need to first add all entries to a manufactured glob entry. Once
			// we read the actual glob entry, we need to find this manufactured
			// glob and mark it complete. If the directory was empty, that glob
			// will not yet exist and we must manufacture it also.
			ge := l.getFromLRU(lruKey{name: e.name, glob: true})
			if ge != nil {
				e.children = ge.children
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

func (l *clog) close() error {
	// Stop go routines.
	close(l.exit)
	<-l.rotaterExited

	// Write out partials.
	var err error
	if l.wr != nil {
		l.wr.Flush()
		err = l.file.Close()
	}
	return err
}

func (l *clog) lookup(name upspin.PathName) (*upspin.DirEntry, error, bool) {
	if *memprofile != "" && string(name) == string(l.cfg.UserName())+"/"+"memstats" {
		dumpMemStats()
	}

	plock := l.pathLocks.lock(name)
	e := l.getFromLRU(lruKey{name: name, glob: false})
	if e != nil {
		de := e.de
		err := e.error
		plock.Unlock()
		return de, err, true
	}
	plock.Unlock()

	// Look for a complete globReq. If there is one and it doesn't list
	// this name, we can return a NotExist error.
	dirName := path.DropPath(name, 1)
	if dirName == name {
		// Name is the root, no glob entry can contain it.
		return nil, nil, false
	}
	glock := l.globLocks.lock(dirName)
	defer glock.Unlock()
	ge := l.getFromLRU(lruKey{name: dirName, glob: true})
	if ge == nil {
		return nil, nil, false
	}
	if !ge.complete {
		return nil, nil, false
	}
	if ge.children[lastElem(name)] {
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

	// Lookup the glob.
	glock := l.globLocks.lock(dirPath)
	defer glock.Unlock()
	e := l.getFromLRU(lruKey{name: dirPath, glob: true})
	if e == nil {
		return nil, nil, false
	}
	if !e.complete {
		return nil, nil, false
	}
	// Lookup all the individual entries.  If any are missing, no go.
	var entries []*upspin.DirEntry
	for n := range e.children {
		name := path.Join(e.name, n)
		plock := l.pathLocks.lock(name)
		ce := l.getFromLRU(lruKey{name: name, glob: false})
		if ce == nil {
			plock.Unlock()
			return nil, nil, false
		}
		if ce.error != nil || ce.de == nil {
			plock.Unlock()
			return nil, nil, false
		}
		entries = append(entries, ce.de)
		plock.Unlock()
	}
	return entries, e.error, true
}

// complete returns true if (1) this was the result of a '*' glob and if
// all its children are still valid in the LRU.
//
// Called with the glob entry locked.
func (l *clog) complete(e *clogEntry) bool {
	if !e.complete {
		return false
	}
	// It's not complete unless all its children are still in the LRU.
	for n := range e.children {
		name := path.Join(e.name, n)
		plock := l.pathLocks.lock(name)
		ce := l.getFromLRU(lruKey{name: name, glob: false})
		if ce == nil {
			plock.Unlock()
			return false
		}
		if ce.error != nil || ce.de == nil {
			plock.Unlock()
			return false
		}
		plock.Unlock()
	}
	return true
}

// globHasAccess looks to see if there is a glob for the name with
// an Access file. it returns:
// - the Access file's DirEntry and true if it does
// - nil and true if the glob definitively has no Access file
// - nil and false if it cannot be determined
func (l *clog) globHasAccess(name upspin.PathName) (*upspin.DirEntry, bool) {
	glock := l.globLocks.lock(name)
	defer glock.Unlock()
	e := l.getFromLRU(lruKey{name: name, glob: true})
	if e == nil {
		// No cached Glob. We don't know.
		return nil, false
	}
	if _, ok := e.children[access.AccessFile]; ok {
		// The directory has a Access file, see if we have a cached DirEntry for it.
		afn := path.Join(name, access.AccessFile)
		plock := l.pathLocks.lock(afn)
		e := l.getFromLRU(lruKey{name: afn, glob: false})
		plock.Unlock()
		if e != nil && e.error == nil && e.de != nil {
			// We have the entry for the access file. Return it.
			return e.de, true
		}
	} else {
		// No access file, is this conplete knowledge?
		if e.complete || e.noAccessFile {
			// We know there is no access file.
			return nil, true
		}
	}

	// We don't know.
	return nil, false
}

// whichAcess returns the applicable Access file to name if one is known.
// We search the cache by walking the path to the root looking for a glob
// entry that contains an Access file. If at any step in the walk, we
// don't have a definitive answer, give up and let the request go to the
// server.
//
// The Access file for a directory can be in the directory itself while the Access
// file for other files can be in its containing directory. That means we have
// to know type of the target is to begin our search. If we can't
// determine that, we have to assume it is a directory and start the search
// for globs of that name.
func (l *clog) whichAccess(name upspin.PathName) (*upspin.DirEntry, error, bool) {
	p, err := path.Parse(name)
	if err != nil {
		log.Debug.Printf("dir/dircache/whichAccess: %s", err)
		return nil, nil, false
	}

	// Is the target in the cache?
	e := l.getFromLRU(lruKey{name: name, glob: false})
	if e != nil {
		// Give up if there was a previous error.
		if e.error != nil || e.de == nil {
			return nil, nil, false
		}

		if e.de.IsLink() {
			return e.de, upspin.ErrFollowLink, true
		}

		// If the target is not a directory, start the search from its parent.
		if !e.de.IsDir() {
			p = p.Drop(1)
		}
	}

	// Walk the cached glob entries to the root looking for the pertinent Access file.
	for {
		de, ok := l.globHasAccess(p.Path())
		if !ok {
			// We don't know enough, ask the actual server.
			return nil, nil, false
		}
		if de != nil {
			// We found an Access file.
			return de, nil, true
		}
		if p.IsRoot() {
			// We walked the whole path with no access file.
			return nil, nil, true
		}
		p = p.Drop(1)
	}
}

func (l *clog) logRequest(op request, name upspin.PathName, err error, de *upspin.DirEntry) {
	l.logRequestWithSequence(op, name, err, de, 0)
}

func (l *clog) logRequestWithSequence(op request, name upspin.PathName, err error, de *upspin.DirEntry, sequence int64) {
	if !cacheableError(err) {
		return
	}

	e := &clogEntry{
		name:     name,
		request:  op,
		error:    err,
		de:       de,
		sequence: sequence,
	}
	l.appendToLogFile(e)
	l.updateLRU(e)

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
		l.appendToLogFile(e)
		l.updateLRU(e)
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
	if strings.ContainsAny(string(pp), "*?[") {
		return p, false
	}
	return pp, true
}

func (l *clog) logGlobRequest(pattern upspin.PathName, err error, entries []*upspin.DirEntry) {
	if !cacheableError(err) {
		return
	}
	dirName, ok := cacheableGlob(pattern)
	if !ok {
		return
	}

	// Log each entry.
	children := make(map[string]bool)
	for _, de := range entries {
		children[lastElem(de.Name)] = true
		l.logRequest(lookupReq, de.Name, err, de)
	}

	// If any files have disappeared from a preexisting glob, remove them.
	glock := l.globLocks.lock(dirName)
	oe := l.getFromLRU(lruKey{name: dirName, glob: true})
	var todelete []*clogEntry
	if oe != nil {
		for n := range oe.children {
			if !children[n] {
				e := &clogEntry{request: deleteReq, name: path.Join(dirName, n)}
				todelete = append(todelete, e)
			}
		}
	}

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
	l.appendToLogFile(e)
	glock.Unlock()
	l.updateLRU(e)
}

// updateLRU adds the entry to the in core LRU version of the clog. We don't remember errors
// in the LRU (other than ErrFollowLink). However, we do use them to remove things from the LRU.
//
// updateLRU returns non zero if the state was changed other than updated refresh times.
func (l *clog) updateLRU(e *clogEntry) {
	if e.error != nil {
		// Remember links.  All cases are equivalent, i.e., treat them like a lookup.
		if e.error == upspin.ErrFollowLink {
			e.request = lookupReq
			l.addToLRU(e)
			l.addToGlob(e)
			return
		}
		if !errors.Is(errors.NotExist, e.error) {
			log.Debug.Printf("updateLRU %s error %s", e.name, e.error)
			return
		}
		l.removeFromLRU(e, true)
		l.removeFromLRU(e, false)
		l.removeFromGlob(e)

		// Add back in as a non-existent file.
		e.request = lookupReq
		l.addToLRU(e)
		return
	}

	// At this point, only requests have gotten through.
	switch e.request {
	case deleteReq:
		l.removeFromLRU(e, true)
		l.removeFromLRU(e, false)
		l.removeFromGlob(e)
	case putReq:
		l.addToLRU(e)
		l.addToGlob(e)
	case lookupReq, globReq:
		l.addToLRU(e)
		l.addToGlob(e)
	case whichAccessReq:
		// Log the access file itself as a lookup. This adds the
		// Access file to the glob of its parent directory and
		// then calls fixAccess to set noAccessFile in all
		// directories between the Access file and the file it
		// is an Access file for.
		if e.de != nil {
			ae := &clogEntry{
				request: lookupReq,
				name:    e.de.Name,
				error:   nil,
				de:      e.de,
			}
			l.addToLRU(ae)
			l.addToGlob(ae)
			l.fixAccess(e)
		}
	case obsoleteReq:
		// These never get logged. They are just markers that the file
		// is of interest to the watcher.
	default:
		log.Printf("unknown request type: %s", e)
	}
}

// addToLRU adds an entry to the LRU.
func (l *clog) addToLRU(e *clogEntry) {
	k := lruKey{name: e.name, glob: e.request == globReq}
	l.lru.Add(k, e)
}

// removeFromLRU removes an entry from the LRU.
func (l *clog) removeFromLRU(e *clogEntry, isGlob bool) {
	l.lru.Remove(lruKey{name: e.name, glob: isGlob})
}

// getFromLRU looks up an entry and returns it if not obsolete.
func (l *clog) getFromLRU(k lruKey) *clogEntry {
	v, ok := l.lru.Get(k)
	if !ok {
		return nil
	}
	e := v.(*clogEntry)
	if e.request == obsoleteReq {
		return nil
	}
	return e
}

// addToGlob creates the glob if it doesn't exist and adds an entry to it.
func (l *clog) addToGlob(e *clogEntry) {
	dirName := path.DropPath(e.name, 1)
	if dirName == e.name {
		return
	}
	k := lruKey{name: dirName, glob: true}
	glock := l.globLocks.lock(dirName)
	defer glock.Unlock()
	ge := l.getFromLRU(k)
	if ge == nil {
		// When creating a glob entry this way, we don't know all of
		// its children so it is incomplete.
		ge = &clogEntry{
			request:  globReq,
			name:     dirName,
			children: make(map[string]bool),
			complete: false,
		}
		l.lru.Add(k, ge)
	}
	lelem := lastElem(e.name)
	if lelem == access.AccessFile {
		ge.noAccessFile = false
	}
	ge.children[lelem] = true
}

// removeFromGlob removes an entry from a glob, should that glob exist.
func (l *clog) removeFromGlob(e *clogEntry) {
	dirName := path.DropPath(e.name, 1)
	if dirName == e.name {
		return
	}
	lelem := lastElem(e.name)
	k := lruKey{name: dirName, glob: true}
	glock := l.globLocks.lock(dirName)
	if ge := l.getFromLRU(k); ge != nil {
		delete(ge.children, lelem)
		if lelem == access.AccessFile {
			ge.noAccessFile = true
		}
	}
	glock.Unlock()
}

// fixAccess sets noAccessFile in all glob entries between the e.name and e.de.Name.
func (l *clog) fixAccess(e *clogEntry) {
	p, err := path.Parse(e.name)
	if err != nil {
		log.Debug.Printf("dir/dirCache/fixAccess: %s", err)
		return
	}

	// The DirEntry for the Access file.
	accessName := e.de.Name

	// Walk the tree marking cached Glob entries
	// with noAccessFile = true.
	for {
		dirName := p.Path()
		if len(dirName) < len(accessName) {
			break
		}
		e := l.getFromLRU(lruKey{name: dirName, glob: true})
		if e != nil {
			e.noAccessFile = true
		}
		p = p.Drop(1)
	}
}

// inSequence returns true if the sequence number is a valid new
// sequence number for name while updating the cached sequence.
func (l *clog) inSequence(name upspin.PathName, seq int64) bool {
	if v, ok := l.sequenceLRU.Get(name); ok {
		if v.(int64) >= seq {
			return false
		}
	}
	l.sequenceLRU.Add(name, seq)
	return true
}

// invalidate invalidates an entry but leaves it in the LRU to remember
// our interest in it.
func (e *clogEntry) invalidate() {
	// Remember that we are still intereseted should a watch
	// return this entry but mark it invalid.
	if e.request != globReq {
		e.request = obsoleteReq
	} else {
		e.complete = false
	}
}

// appendToLogFile appends to the clog file.
func (l *clog) appendToLogFile(e *clogEntry) error {
	if e.request == obsoleteReq {
		log.Info.Printf("appendToLogFile: obsolete requests should not be written to log")
		return nil
	}

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
		return errors.Is(errors.NotExist, e)
	}
	return err == upspin.ErrFollowLink
}

var tooShort = errors.E(errors.Invalid, "log entry too short")
var tooLong = errors.E(errors.Invalid, "log entry too long")
var badVersion = errors.E(errors.Invalid, "bad log file version")

// A marshalled entry is of the form:
//   request-type: byte
//   sequence: varint
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
	// request
	if e.request >= maxReq {
		return nil, errors.Errorf("unknown clog operation %d", e.request)
	}
	if e.request == globReq && !e.complete {
		return nil, nil
	}
	b := []byte{byte(e.request)}

	// sequence
	var tmp [16]byte
	n := binary.PutVarint(tmp[:], e.sequence)
	b = append(b, tmp[:n]...)

	// error
	b = appendError(b, e.error)

	// de
	var err error
	b, err = appendDirEntry(b, e.de)
	if err != nil {
		return nil, err
	}

	// name
	if e.de == nil {
		b = appendString(b, string(e.name))
	}

	// children
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
	// request
	e.request = request(b[0])
	if e.request >= maxReq {
		return errors.E(errors.Invalid, errors.Errorf("unknown clog operation %d", e.request))
	}
	b = b[1:]

	// sequence
	var n int
	e.sequence, n = binary.Varint(b)
	if n == 0 {
		return tooShort
	}
	b = b[n:]

	// error
	if e.error, b, err = getError(b); err != nil {
		return err
	}

	// de
	if e.de, b, err = getDirEntry(b); err != nil {
		return err
	}

	// name
	if e.de == nil {
		var str string
		if str, b, err = getString(b); err != nil {
			return err
		}
		e.name = upspin.PathName(str)
	} else {
		e.name = e.de.Name
	}

	// children
	if e.request == globReq {
		if e.children, b, err = getChildren(b); err != nil {
			return err
		}
		e.complete = true
	}
	if len(b) != 0 {
		return errors.E(errors.Invalid, "log entry too long")
	}
	return
}

// read reads a single entry from the clog and unmarshals it.
func (e *clogEntry) read(l *clog, rd *bufio.Reader) error {
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

	// If sequence is set, update the sequence in the proxied directories.
	if e.sequence != 0 {
		l.proxied.setSequence(e.name, e.sequence)
		e.sequence = 0
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

var reqName = map[request]string{
	lookupReq:      "lookup",
	globReq:        "glob",
	deleteReq:      "delete",
	putReq:         "put",
	whichAccessReq: "whichAccess",
	versionReq:     "version",
	obsoleteReq:    "obsolete",
}

func (e *clogEntry) String() string {
	rv := "?"
	if e.request >= lookupReq && e.request < maxReq {
		rv = reqName[e.request]
	}
	rv += fmt.Sprintf(" %s ", e.name)
	if e.sequence != 0 {
		rv += fmt.Sprintf(" sequence<%d>", e.sequence)
	}
	if e.error != nil {
		rv += fmt.Sprintf(" error<%s>", e.error)
	}
	if e.de != nil {
		dtype := "file"
		if e.de.IsDir() {
			dtype = "dir"
		} else if e.de.IsLink() {
			dtype = "link"
		}
		rv += fmt.Sprintf(" de<%s, %s, %s, %d>", e.de.Name, dtype, e.de.Link, e.de.Sequence)
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
