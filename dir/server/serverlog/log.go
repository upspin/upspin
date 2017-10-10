// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serverlog maintains logs for directory servers, permitting
// replay, recovering, and mirroring.
package serverlog

// This file defines and implements three components for record keeping for a
// Tree:
//
// 1) writer - writes log entries to the end of the log file.
// 2) Reader - reads log entries from any offset of the log file.
// 3) checkpoint - saves the most recent commit point in the log and the root.
//
// The structure on disk is, relative to a log directory:
//
// tree.root.<username>  - root entry for username
// tree.index.<username> - log checkpoint for username (historically named).
// d.tree.log.<username> - subdirectory for username, containing files named:
// <offset>.<version> - log greater than offset but less than the next offset file.
// The .version part is missing for old-format logs.
//
// There may also be a legacy file tree.log.<username> which will be renamed
// (and set to offset 0) if found.
//

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/valid"
)

// User holds the log state for a single user.
type User struct {
	name      upspin.UserName
	directory string
	storage   storage.Storage

	// mu locks all writes to the writer and checkpoint,
	// and the offSeqs structure. Readers have their
	// own lock. A pointer so its clones share the lock
	// (see ReadOnlyClone).
	mu *sync.Mutex

	writer     *writer
	checkpoint *checkpoint

	// files are sorted in increasing offset order.
	files []*logFile

	// Kept in increasing sequence order.
	// TODO: Make this a sparse slice and do small linear scans.
	offSeqs []offSeq

	// savedRootSeq remembers the sequence number of the
	// last root saved to the root file.
	savedRootSeq int64
}

// Operation is the kind of operation performed on the DirEntry.
type Operation int

// Operations on dir entries that are logged.
const (
	Put Operation = iota
	Delete
)

// MaxLogSize is the maximum size of a single log file.
// It can be modified, such as for testing.
var MaxLogSize int64 = 100 * 1024 * 1024 // 100 MB

// Entry is the unit of logging.
type Entry struct {
	Op    Operation
	Entry upspin.DirEntry
}

// writer is an append-only log of Entry.
type writer struct {
	user *User // owner of this writer.

	fd   *os.File // file descriptor for the log.
	file *logFile // log this writer is writing to.
}

// Write implements io.Writer for the our User type.
// It is the method clients use to append data to the set of log files.
// TODO: Used only in a test of corrupted data in ../tree - could be deleted.
func (u *User) Write(b []byte) (int, error) {
	return u.writer.fd.Write(b)
}

// Reader reads LogEntries from the log.
type Reader struct {
	// user owns the log.
	user *User

	// mu protects the fields below. If user.mu must be held, it must be
	// held after mu.
	mu sync.Mutex

	fd   *os.File // file descriptor for the log.
	file *logFile // log this writer is writing to.
}

// checkpoint reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress
// processing the log and storing the root.
type checkpoint struct {
	user *User // owner of this checkpoint.

	checkpointFile *os.File // file descriptor for the checkpoint.
}

// offSeq remembers the correspondence between a global offset
// for a user and the sequence number of the change at that offset.
type offSeq struct {
	offset   int64
	sequence int64
}

// logFile gathers the information about a log file on disk.
type logFile struct {
	name    string // Full path name.
	index   int    // Position in User.files.
	version int    // Version number of the format used.
	offset  int64  // Offset at start of file.
}

const (
	// Version 0 refers to the old logs that did not have a version number
	// in their name.
	version               = 1
	oldStyleLogFilePrefix = "tree.log."
	// Version 0 logs had 23 low bits of actual sequence; the upper
	// bits were random. When we read version 0 logs, we clear
	// the random bits.
	version0SeqMask = 1<<23 - 1
)

// Open returns a User structure holding the open
// logs for the user in the named local file system's directory.
// If the user does not already have logs in this directory, Open
// will create them.
//
// Only one User can be opened for a given user in a given directory
// or logs could be corrupted. It is the caller's responsibility to
// provide this guarantee.
func Open(userName upspin.UserName, directory string, store storage.Storage) (*User, error) {
	if err := valid.UserName(userName); err != nil {
		return nil, err
	}

	u := &User{
		name:      userName,
		directory: directory,
		storage:   store,
		mu:        new(sync.Mutex),
	}
	subdir := u.logSubDir()

	// Make the log directory if it doesn't exist.
	// (MkdirAll returns a nil error if the directory exists.)
	if err := os.MkdirAll(subdir, 0700); err != nil {
		return nil, errors.E(errors.IO, err)
	}

	// If there's an old log, move it.
	// TODO: Remove this code once all users are updated, or by June 2018.
	oldLogName := filepath.Join(directory, oldStyleLogFilePrefix+string(userName))
	if _, err := os.Stat(oldLogName); err == nil {
		err := moveIfNotExist(oldLogName, u.logFileName(0, 0))
		if err != nil {
			return nil, errors.E(errors.IO, err)
		}
		// If we've reached this point then we've either moved the old log file
		// to its new location, or it was previously hard-linked as log entry
		// zero. In either case, just blindly try to delete the old log file.
		// We don't need it anymore.
		os.Remove(oldLogName)
	}

	u.findLogFiles(subdir)

	// Create user's first log if none exists.
	var (
		fd  *os.File
		err error
	)
	last := len(u.files) - 1
	switch {
	case len(u.files) == 0:
		// No files for this user yet.
		_, fd, err = u.createLogFile(0)
	case u.files[last].version != version:
		// Must create new file with current version.
		// We can only write to files with the latest version.
		file := u.files[last]
		size, err := sizeOfFile(file.name)
		if err != nil {
			break
		}
		_, fd, err = u.createLogFile(file.offset + size)
		fd, err = os.OpenFile(u.files[len(u.files)-1].name, os.O_APPEND|os.O_WRONLY, 0600)
	case u.files[last].version > version:
		// Cannot happen!
		return nil, errors.E(errors.Internal, errors.Errorf("bad version number for log file %q", u.files[last].name))
	default:
		// Things are normal.
		fd, err = os.OpenFile(u.files[len(u.files)-1].name, os.O_APPEND|os.O_WRONLY, 0600)
	}
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}

	u.checkpoint, err = newCheckpoint(u)
	if err != nil {
		return nil, err
	}

	w := &writer{
		user: u,
		fd:   fd,
		file: u.files[len(u.files)-1],
	}
	u.writer = w

	return u, nil
}

func newCheckpoint(u *User) (*checkpoint, error) {
	f, err := os.OpenFile(u.checkpointFile(), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	return &checkpoint{
		user:           u,
		checkpointFile: f,
	}, nil
}

// ReadOnlyClone returns a copy of the user structure with no writer,
// creating a read-only accessor for the logs.
func (u *User) ReadOnlyClone() (*User, error) {
	clone := *u
	clone.writer = nil
	var err error
	clone.checkpoint, err = u.checkpoint.readOnlyClone()
	if err != nil {
		return nil, err
	}
	return &clone, nil
}

// moveIfNotExist moves src to dst if dst does not yet exist.
// Otherwise it does nothing. If src does not exist, it does nothing.
func moveIfNotExist(src, dst string) error {
	_, err := os.Stat(dst)
	if err == nil {
		// Target already exists, nothing to do.
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err = os.Stat(src)
	if os.IsNotExist(err) {
		// Source does not exist, nothing to do.
		return nil
	}
	if err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// HasLog reports whether user has logs in its directory.
func HasLog(user upspin.UserName, directory string) (bool, error) {
	var firstErr error
	u := &User{
		name:      user,
		directory: directory,
	}
	for _, name := range []string{
		filepath.Join(directory, oldStyleLogFilePrefix+string(user)),
		u.logSubDir(),
	} {
		_, err := os.Stat(name)
		if err != nil {
			if !os.IsNotExist(err) && firstErr != nil {
				firstErr = errors.E(errors.IO, err)
			}
			continue
		}
		return true, nil
	}
	return false, firstErr
}

// DeleteLogs deletes all data for a user in its directory. Any existing logs
// associated with user must not be used subsequently.
func (u *User) DeleteLogs() error {
	for _, fn := range []string{
		filepath.Join(u.directory, oldStyleLogFilePrefix+string(u.name)),
		u.rootFile(),
		u.checkpointFile(),
	} {
		err := os.Remove(fn)
		if err != nil && !os.IsNotExist(err) {
			return errors.E(errors.IO, err)
		}
	}
	// Remove the user's log directory, if any, with all its contents.
	// Note: RemoveAll returns nil if the subdir does not exist.
	err := os.RemoveAll(u.logSubDir())
	if err != nil && !os.IsNotExist(err) {
		return errors.E(errors.IO, err)
	}
	u.savedRootSeq = 0
	return nil
}

// userGlob returns the set of users in the directory that match the pattern.
// The pattern is as per filePath.Glob, applied to the directory.
func userGlob(pattern string, directory string) ([]upspin.UserName, error) {
	prefix := filepath.Join(directory, checkpointFilePrefix)
	matches, err := filepath.Glob(prefix + pattern)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	users := make([]upspin.UserName, len(matches))
	for i, m := range matches {
		users[i] = upspin.UserName(strings.TrimPrefix(m, prefix))
	}
	return users, nil
}

// ListUsers returns all user names found in the given log directory.
func ListUsers(directory string) ([]upspin.UserName, error) {
	return userGlob("*@*", directory)
}

// ListUsersWithSuffix returns a list is user names found in the given log
// directory that contain the required suffix, without the leading "+".
// The special suffix "*" matches all users with a non-empty suffix.
func ListUsersWithSuffix(suffix, directory string) ([]upspin.UserName, error) {
	return userGlob("*+"+suffix+"@*", directory)
}

func (u *User) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	err1 := u.writer.close()
	err2 := u.checkpoint.close()
	if err1 != nil {
		return err1
	}
	return err2
}

func (u *User) Name() upspin.UserName {
	return u.name
}

func (u *User) logFileName(offset int64, version int) string {
	// Version 0 logs don't have a .0 at the end.
	if version == 0 {
		return filepath.Join(u.logSubDir(), fmt.Sprintf("%d", offset))
	}
	return filepath.Join(u.logSubDir(), fmt.Sprintf("%d.%d", offset, version))
}

func (u *User) logSubDir() string {
	return filepath.Join(u.directory, "d.tree.log."+string(u.name))
}

const (
	rootFilePrefix = "tree.root."
	// For historical reasons, the checkpoint file name is "index".
	checkpointFilePrefix = "tree.index."
)

func (u *User) checkpointFile() string {
	return filepath.Join(u.directory, checkpointFilePrefix+string(u.name))
}

func (u *User) rootFile() string {
	return filepath.Join(u.directory, rootFilePrefix+string(u.name))
}

func (u *User) rootFileRef() string {
	return rootFilePrefix + string(u.name)
}

// findLogFiles populates u.files with the log files available for this user.
// They are stored in increasing offset order.
func (u *User) findLogFiles(dir string) {
	u.files = nil // Safety; shouldn't be necessary.
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil || len(files) == 0 {
		return
	}
	for _, file := range files {
		// Format of name is ..../*tree.log.ann@example.com/oooo.vvvv where o=offset, v=version.
		// For old files, .vvvv will be missing, and version is 0.
		elems := strings.Split(filepath.Base(file), ".")
		var ints []int64
		for _, elem := range elems {
			x, err := strconv.ParseInt(elem, 10, 64)
			if err != nil {
				log.Error.Printf("serverlog.findLogFiles: can't parse %q", file)
				continue
			}
			ints = append(ints, x)
		}
		lf := &logFile{
			name:  file,
			index: len(u.files),
		}
		switch len(ints) {
		case 2:
			lf.version = int(ints[1])
			fallthrough
		case 1:
			lf.offset = ints[0]
		default:
			log.Error.Printf("serverlog.findLogFiles: can't parse %q", file)
			continue
		}
		u.files = append(u.files, lf)
	}
	sort.Slice(u.files, func(i, j int) bool { return u.files[i].offset < u.files[j].offset })

}

// createLogFile creates a file for the offset and returns the logFile and open fd.
func (u *User) createLogFile(offset int64) (*logFile, *os.File, error) {
	name := u.logFileName(offset, version)
	fd, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, err
	}
	lf := &logFile{
		name:    name,
		index:   len(u.files),
		version: version,
		offset:  offset,
	}
	u.files = append(u.files, lf)
	return lf, fd, err
}

// isWriter reports whether the index is that of the most recent log file.
// It's used to permit Reader to check whether it will interfere with writer.
func (u *User) isWriter(file *logFile) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return file == u.writer.file
}

// whichLogFile returns the log file to use to read this offset.
// u.mu must be held.
func (u *User) whichLogFile(offset int64) *logFile {
	for i := 1; i < len(u.files); i++ {
		if u.files[i].offset > offset {
			return u.files[i-1]
		}
	}
	return u.files[len(u.files)-1]
}

// OffsetOf returns the global offset in the user's logs for this sequence number.
// It returns -1 if the sequence number does not appear in the logs.
// ReadAt will return an error if asked to read at a negative offset.
func (u *User) OffsetOf(seq int64) int64 {
	if seq == 0 {
		// Start of file. There may be no data yet.
		// TODO: How does this arise? (It does, but it shouldn't.)
		return 0
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	i := sort.Search(len(u.offSeqs), func(i int) bool { return u.offSeqs[i].sequence >= seq })
	if i < len(u.offSeqs) && u.offSeqs[i].sequence == seq {
		return u.offSeqs[i].offset
	}
	return -1
}

// Append appends a Entry to the end of the writer log.
func (u *User) Append(e *Entry) error {
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	w := u.writer

	prevSize := size(w.fd)
	offset := w.file.offset + prevSize

	// Is it time to move to a new log file?
	if prevSize >= MaxLogSize {
		// Close the current underlying log file.
		err = w.close()
		if err != nil {
			return errors.E(errors.IO, err)
		}
		// Create a new log file where the previous one left off.
		file, fd, err := u.createLogFile(w.file.offset + prevSize)
		if err != nil {
			return errors.E(errors.IO, err)
		}
		w.file = file
		w.fd = fd
		prevSize = 0
	}

	// File is append-only, so this is guaranteed to write to the tail.
	n, err := w.fd.Write(buf)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	err = w.fd.Sync()
	if err != nil {
		return errors.E(errors.IO, err)
	}
	// Sanity check: flush worked and the new offset relative to the
	// beginning of this file is the expected one.
	newOffs := prevSize + int64(n)
	if newOffs != size(w.fd) {
		// This might indicate a race somewhere, despite the locks.
		return errors.E(errors.IO, errors.Errorf("file.Sync did not update offset: expected %d, got %d", newOffs, size(w.fd)))
	}

	u.addOffSeq(offset, e.Entry.Sequence)
	return nil
}

// addOffSeq remembers an offset/sequence pair.
func (u *User) addOffSeq(offset, sequence int64) {
	// The offSeqs slice must be kept in Sequence order, which might not be
	// in offset order if there is concurrent access. We could sort the list but
	// the invariant is that it's sorted when we get here, so all we need to do
	// is insert the new record in the right place. Moreover, it will be near
	// the end so it's fastest just to scan backwards.
	var i int
	for i = len(u.offSeqs); i > 0; i-- {
		if u.offSeqs[i-1].sequence <= sequence {
			break
		}
	}
	u.offSeqs = append(u.offSeqs, offSeq{})
	copy(u.offSeqs[i+1:], u.offSeqs[i:])
	u.offSeqs[i] = offSeq{
		offset:   offset,
		sequence: sequence,
	}
}

// ReadAt reads an entry from the log at offset. It returns the log entry and
// the next offset. If offset is negative, which may correspond to an invalid
// sequence number processed by OffsetOf, it returns an error.
func (r *Reader) ReadAt(offset int64) (le Entry, next int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The maximum offset we can satisfy with the current log file.
	maxOff := r.file.offset + size(r.fd)

	// Is the requested offset outside the bounds of the current log file?
	before := offset < r.file.offset
	after := offset >= maxOff
	if before || after {
		// Locate the file and open it.
		r.user.mu.Lock()
		err := r.openLogForOffset(offset)
		r.user.mu.Unlock()
		if err != nil {
			return le, 0, errors.E(errors.IO, err)
		}
		// Recompute maxOff for the new file.
		maxOff = r.file.offset + size(r.fd)
	}

	// If we're reading from the log file being written, then we
	// must lock r.user.mu to avoid reading partially-written data.
	if r.user.isWriter(r.file) {
		r.user.mu.Lock()
		defer r.user.mu.Unlock()
	}

	// Are we past the end of the current file?
	if offset >= maxOff {
		return le, maxOff, nil
	}

	_, err = r.fd.Seek(offset-r.file.offset, io.SeekStart)
	if err != nil {
		return le, 0, errors.E(errors.IO, err)
	}
	next = offset
	checker := newChecker(r.fd)
	defer checker.close()

	err = le.unmarshal(checker)
	if err != nil {
		return le, next, err
	}
	next = next + int64(checker.count)
	if r.file.version == 0 {
		le.Entry.Sequence &= version0SeqMask
	}
	return
}

// AppendOffset returns the offset of the end of the written log file or -1 on error.
func (u *User) AppendOffset() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	w := u.writer
	return w.file.offset + size(w.fd)
}

// EndOffset returns the offset of the end of the current file or -1 on error.
// TODO: Used only in a test in ../tree. Could be deleted.
func (r *Reader) EndOffset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If we're reading from the same file as the current writer, lock it.
	// Order is important.
	if r.file.offset == r.user.files[len(r.user.files)-1].offset {
		r.user.mu.Lock()
		defer r.user.mu.Unlock()
	}

	return r.file.offset + size(r.fd)
}

// size returns the offset at the end of the file or -1 on error.
// The file must be changed simultaneously with this call.
func size(f *os.File) int64 {
	fi, err := f.Stat()
	if err != nil {
		return -1
	}
	return fi.Size()
}

// sizeOfFile returns the offset at the end of the named file.
func sizeOfFile(name string) (int64, error) {
	fi, err := os.Stat(name)
	return fi.Size(), err
}

// Truncate truncates the write log at offset.
func (u *User) Truncate(offset int64) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Delete any files after the one holding offset.
	file := u.whichLogFile(offset)
	for i := file.index + 1; i < len(u.files); i++ {
		err := os.Remove(u.files[i].name)
		if err != nil {
			return errors.E(errors.IO, err)
		}
	}
	u.files = u.files[:file.index+1]

	// Move the writer to that file, if not already there.
	w := u.writer
	if w.file != file {
		if err := w.close(); err != nil {
			return errors.E(errors.IO, err)
		}
		fd, err := os.OpenFile(file.name, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return errors.E(errors.IO, err)
		}
		w.file = file
		w.fd = fd
	}

	// Truncate the active file.
	pos := offset - w.file.offset
	if pos < size(w.fd) {
		err := w.fd.Truncate(pos)
		if err != nil {
			return errors.E(errors.IO, err)
		}
		w.fd.Seek(pos, io.SeekStart)
	}
	u.truncateOffSeqs(offset)
	return nil
}

// truncateOffSeqs truncates the offSeqs list at the specified offset. u.mu must be locked.
func (u *User) truncateOffSeqs(offset int64) {
	i := sort.Search(len(u.offSeqs), func(i int) bool { return u.offSeqs[i].offset >= offset })
	if i >= len(u.offSeqs) {
		/* Nothing to do */
		return
	}
	// Make a copy to save what might be a lot of memory. Append will add some headroom.
	u.offSeqs = append([]offSeq{}, u.offSeqs[:i]...)
}

// NewReader makes a reader of the user's log.
func (u *User) NewReader() (*Reader, error) {
	r := &Reader{}

	// Order is important.
	r.mu.Lock()
	defer r.mu.Unlock()

	u.mu.Lock()
	defer u.mu.Unlock()

	w := u.writer
	r.user = u

	if w.fd == nil {
		panic("nil writer")
	}
	err := r.openLogForOffset(w.file.offset)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	return r, nil
}

// openLogForOffset opens the log file that holds the offset.
// r.mu must be held.
func (r *Reader) openLogForOffset(offset int64) error {
	logFile := r.user.whichLogFile(offset)
	// Re-opening the same offset?
	if r.fd != nil && r.fd.Name() == logFile.name {
		return nil
	}
	f, err := os.Open(logFile.name)
	if err != nil {
		return err
	}
	if r.fd != nil {
		r.fd.Close()
	}
	r.fd = f
	r.file = logFile
	return nil
}

// close closes the writer. user.mu must be held.
func (w *writer) close() error {
	if w == nil || w.fd == nil {
		return nil
	}
	err := w.fd.Close()
	w.fd = nil
	return err
}

// Close closes the reader.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fd != nil {
		err := r.fd.Close()
		r.fd = nil
		return err
	}
	return nil
}

// Root returns the user's root by retrieving it from local stable storage.
func (u *User) Root() (*upspin.DirEntry, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	buf, err := u.storage.Download(u.rootFileRef())
	isNotExist := errors.Match(errors.E(errors.NotExist), err)
	if err != nil && !isNotExist {
		return nil, err
	}
	if isNotExist {
		// Try reading the root from its old location on local disk.
		buf, err = ioutil.ReadFile(u.rootFile())
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.E(errors.IO, err)
		}
	}
	if len(buf) == 0 {
		return nil, errors.E(errors.NotExist, u.Name(), errors.Str("no root for user"))
	}
	var root upspin.DirEntry
	more, err := root.Unmarshal(buf)
	if err != nil {
		return nil, err
	}
	if len(more) != 0 {
		return nil, errors.E(errors.IO, errors.Errorf("root has %d left over bytes", len(more)))
	}
	u.savedRootSeq = root.Sequence
	return &root, nil
}

// SaveRoot saves the user's root entry to stable storage.
func (u *User) SaveRoot(root *upspin.DirEntry) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.savedRootSeq == root.Sequence {
		return nil
	}

	buf, err := root.Marshal()
	if err != nil {
		return err
	}
	err = u.storage.Put(u.rootFileRef(), buf)
	if err != nil {
		return err
	}
	u.savedRootSeq = root.Sequence
	return nil
}

// DeleteRoot deletes the root.
func (u *User) DeleteRoot() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.storage.Delete(u.rootFileRef()); err != nil {
		return err
	}
	u.savedRootSeq = 0
	return nil
}

// readOnlyClone makes a read-only copy of the checkpoint.
func (cp *checkpoint) readOnlyClone() (*checkpoint, error) {
	cp.user.mu.Lock()
	defer cp.user.mu.Unlock()

	fd, err := os.Open(cp.checkpointFile.Name())
	if os.IsNotExist(err) {
		return nil, errors.E(errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	newCp := *cp
	newCp.checkpointFile = fd
	return &newCp, nil
}

func overwriteAndSync(f *os.File, buf []byte) error {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	n, err := f.Write(buf)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	err = f.Truncate(int64(n))
	if err != nil {
		return errors.E(errors.IO, err)
	}
	return f.Sync()
}

func readAllFromTop(f *os.File) ([]byte, error) {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	return buf, nil
}

// ReadOffset reads from stable storage the offset saved by SaveOffset.
func (u *User) ReadOffset() (int64, error) {
	return u.checkpoint.readOffset()
}

// readOffset reads from stable storage the offset saved by SaveOffset.
func (cp *checkpoint) readOffset() (int64, error) {
	cp.user.mu.Lock()
	defer cp.user.mu.Unlock()

	buf, err := readAllFromTop(cp.checkpointFile)
	if err != nil {
		return 0, errors.E(errors.IO, err)
	}
	if len(buf) == 0 {
		return 0, errors.E(errors.NotExist, cp.user.Name(), errors.Str("no log offset for user"))
	}
	offset, n := binary.Varint(buf)
	if n <= 0 {
		return 0, errors.E(errors.IO, errors.Str("invalid offset read"))
	}
	return offset, nil
}

// SaveOffset saves to stable storage the offset to process next.
func (u *User) SaveOffset(offset int64) error {
	return u.checkpoint.saveOffset(offset)
}

// saveOffset saves to stable storage the offset to process next.
func (cp *checkpoint) saveOffset(offset int64) error {
	if offset < 0 {
		return errors.E(errors.Invalid, errors.Str("negative offset"))
	}
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], offset)

	cp.user.mu.Lock()
	defer cp.user.mu.Unlock()

	return overwriteAndSync(cp.checkpointFile, tmp[:n])
}

// close closes the checkpoint. user.mu must be held
func (cp *checkpoint) close() error {
	var firstErr error
	if cp.checkpointFile != nil {
		firstErr = cp.checkpointFile.Close()
		cp.checkpointFile = nil
	}
	return firstErr
}

// marshal packs the Entry into a new byte slice for storage.
func (le *Entry) marshal() ([]byte, error) {
	var b []byte
	var tmp [16]byte // For use by PutVarint.
	// This should have been b = append(b, byte(le.Op)) since Operation
	// is known to fit in a byte. However, we already encode it with
	// Varint and changing it would cause backward-incompatible issues.
	n := binary.PutVarint(tmp[:], int64(le.Op))
	b = append(b, tmp[:n]...)

	entry, err := le.Entry.Marshal()
	if err != nil {
		return nil, err
	}
	b = appendBytes(b, entry)
	chksum := checksum(b)
	b = append(b, chksum[:]...)
	return b, nil
}

func checksum(buf []byte) [4]byte {
	var c [4]byte
	copy(c[:], checksumSalt[:])
	for i, b := range buf {
		c[i%4] ^= b
	}
	return c
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(bytes)))
	b = append(b, tmp[:n]...)
	b = append(b, bytes...)
	return b
}

var checksumSalt = [4]byte{0xde, 0xad, 0xbe, 0xef}

// checker computes the checksum of a file as it reads bytes from it. It also
// reports the number of bytes read in its count field.
type checker struct {
	rd     *bufio.Reader
	count  int
	chksum [4]byte
}

var pool sync.Pool

func newChecker(r io.Reader) *checker {
	var chk *checker
	if c, ok := pool.Get().(*checker); c != nil && ok {
		chk = c
		chk.reset(r)
	} else {
		chk = &checker{rd: bufio.NewReader(r), chksum: checksumSalt}
	}
	return chk
}

// ReadByte implements io.ByteReader.
func (c *checker) ReadByte() (byte, error) {
	b, err := c.rd.ReadByte()
	if err == nil {
		c.chksum[c.count%4] = c.chksum[c.count%4] ^ b
		c.count++
	}
	return b, err
}

// resetChecksum resets the checksum and the counting of bytes, without
// affecting the reader state.
func (c *checker) resetChecksum() {
	c.count = 0
	c.chksum = checksumSalt
}

// reset clears all internal state: clears count, checksum and any buffering.
func (c *checker) reset(rd io.Reader) {
	c.rd.Reset(rd)
	c.resetChecksum()
}

// close closes the checker and releases internal storage. Future uses of it are
// invalid.
func (c *checker) close() {
	c.rd.Reset(nil)
	pool.Put(c)
}

// Read implements io.Reader.
func (c *checker) Read(p []byte) (n int, err error) {
	n, err = c.rd.Read(p)
	if err != nil {
		return
	}
	for i := 0; i < n; i++ {
		offs := (c.count + i) % 4
		c.chksum[offs] = c.chksum[offs] ^ p[i]
	}
	c.count += n
	return
}

func (c *checker) readChecksum() ([4]byte, error) {
	var chk [4]byte

	n, err := io.ReadFull(c.rd, chk[:])
	if err != nil {
		return chk, err
	}
	c.count += n
	return chk, nil
}

// unmarshal unpacks a marshaled Entry from a Reader and stores it in the
// receiver.
func (le *Entry) unmarshal(r *checker) error {
	operation, err := binary.ReadVarint(r)
	if err != nil {
		return errors.E(errors.IO, errors.Errorf("reading op: %s", err))
	}
	le.Op = Operation(operation)
	entrySize, err := binary.ReadVarint(r)
	if err != nil {
		return errors.E(errors.IO, errors.Errorf("reading entry size: %s", err))
	}
	// TODO: document this properly. See issue #347.
	const reasonableEntrySize = 1 << 26 // 64MB
	if entrySize <= 0 {
		return errors.E(errors.IO, errors.Errorf("invalid entry size: %d", entrySize))
	}
	if entrySize > reasonableEntrySize {
		return errors.E(errors.IO, errors.Errorf("entry size too large: %d", entrySize))
	}
	// Read exactly entrySize bytes.
	data := make([]byte, entrySize)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return errors.E(errors.IO, errors.Errorf("reading %d bytes from entry: %s", entrySize, err))
	}
	leftOver, err := le.Entry.Unmarshal(data)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	if len(leftOver) != 0 {
		return errors.E(errors.IO, errors.Errorf("%d bytes left; log misaligned for entry %+v", len(leftOver), le.Entry))
	}
	chk, err := r.readChecksum()
	if err != nil {
		return errors.E(errors.IO, errors.Errorf("reading checksum: %s", err))
	}
	if chk != r.chksum {
		return errors.E(errors.IO, errors.Errorf("invalid checksum: got %x, expected %x for entry %+v", r.chksum, chk, le.Entry))
	}
	return nil
}
