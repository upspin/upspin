// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverlog

import (
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

	// mu locks all writes to the writer, root, and checkpoint, and the
	// offSeqs structure. Readers have their own lock. A pointer so its
	// clones share the lock (see ReadOnlyClone).
	mu *sync.Mutex

	writer     *writer
	root       *root
	checkpoint *checkpoint

	// files are sorted in increasing offset order.
	files []*logFile

	// Kept in increasing sequence order.
	// TODO: Make this a sparse slice and do small linear scans.
	offSeqs []offSeq

	// v1transition records the time that the logs switched
	// from version 0 to version 1. If there are no version 0
	// logs, it will be zero.
	v1Transition upspin.Time
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
	// A common buffer to avoid allocation. Too big and it
	// wastes time doing I/O, too small and it misses too
	// many opportunities. 4K seems good - DirEntries
	// can be fairly large.
	// TODO: Do some empirical measurements to help
	// pick the right size.
	data [4096]byte
}

// checkpoint reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress
// processing the log and storing the root.
type checkpoint struct {
	user *User // owner of this checkpoint.

	checkpointFile *os.File // file descriptor for the checkpoint.
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

// root reads and writes the tree root from/to stable storage. Optionally, it
// lazily saves the root to a storage backend for safe keeping.
type root struct {
	readOnly bool     // if true, writes to this root will fail.
	file     *os.File // local file containing the user's root.

	// savedSeq remembers the sequence number of the
	// last root saved to the root file.
	savedSeq int64

	// TODO: Consider whether the reference should be encrypted.
	ref      string    // storage ref for the user's root.
	saveRoot chan bool // signal that the root should be saved.
	saveDone chan bool // closed when saveLoop exits.

	mu   sync.Mutex
	root []byte // contents of the root file.
}

// newRoot creates or opens the given rootFile and manages I/O to that file.
// If a storage.Storage implementation is provided, root lazily stores the
// contents of rootFile to a reference in that storage backend whenever
// rootFile is updated. The given config is used to generate a secret reference
// name for the backup.
func newRoot(rootFile string, fac upspin.Factotum, s storage.Storage) (*root, error) {
	var rootRef string
	var err error
	if s != nil {
		// Use the provided factotum to generate the secret reference.
		if fac == nil {
			return nil, errors.Str("cannot backup root: config has no factotum")
		}
		base := filepath.Base(rootFile)
		rootRef, err = hashRoot(base, fac)
		if err != nil {
			return nil, err
		}

		// Try to access the storage backend now
		// so a misconfiguration is caught at startup.
		_, err = s.Download(rootRef)
		if err != nil && !errors.Is(errors.NotExist, err) {
			return nil, err
		}
	}
	f, err := os.OpenFile(rootFile, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	r := &root{file: f}
	if s != nil {
		r.ref = rootRef
		r.saveRoot = make(chan bool, 1)
		r.saveDone = make(chan bool)
		go r.saveLoop(s)
	}
	return r, nil
}

func hashRoot(base string, fac upspin.Factotum) (string, error) {
	salt := []byte("@@hashRoot!!")
	suffix := make([]byte, 8)
	err := fac.HKDF(salt, []byte(base), suffix)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%x", base, suffix), nil
}

func (r *root) saveLoop(s storage.Storage) {
	defer close(r.saveDone)
	for range r.saveRoot {
		r.mu.Lock()
		buf := r.root
		r.mu.Unlock()
		err := s.Put(r.ref, buf)
		if err != nil {
			log.Error.Printf("dir/server: could not save root to storage backend: %v", err)
			// TODO(adg): do we want to retry on failure?
			// If so, what kinds of failures?
		}
	}
}

func (r *root) close() error {
	if r.saveRoot != nil {
		close(r.saveRoot)
		<-r.saveDone
	}
	return nil
}

func (r *root) readOnlyClone() (*root, error) {
	f, err := os.Open(r.file.Name())
	if err != nil {
		return nil, err
	}
	return &root{
		readOnly: true,
		file:     f,
	}, nil
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

// Open returns a User structure holding the open logs for the user in the
// named local file system's directory. If the user does not already have logs
// in this directory, Open will create them.
//
// If a store is provided then the root will be backed up to that storage
// backend whenever it changes, so that the tree may be recovered in the event
// that the log directory is lost or corrupted. If store is non-nil then the
// provided factotum must also be non-nil, as it is used to geneate the secret
// reference under which the root is backed up.
//
// Only one User can be opened for a given user in a given directory
// or logs could be corrupted. It is the caller's responsibility to
// provide this guarantee.
func Open(userName upspin.UserName, directory string, fac upspin.Factotum, store storage.Storage) (*User, error) {
	if err := valid.UserName(userName); err != nil {
		return nil, err
	}

	u := &User{
		name:      userName,
		directory: directory,
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
	u.populateOffSeqs()
	u.setV1Transition()

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
		var size int64
		size, err = sizeOfFile(file.name)
		if err != nil {
			break
		}
		_, fd, err = u.createLogFile(file.offset + size)
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

	u.root, err = newRoot(u.rootFile(), fac, store)
	if err != nil {
		return nil, err
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

// ReadOnlyClone returns a copy of the user structure with no writer,
// creating a read-only accessor for the logs.
func (u *User) ReadOnlyClone() (*User, error) {
	clone := *u
	clone.writer = nil
	var err error
	clone.root, err = u.root.readOnlyClone()
	if err != nil {
		return nil, err
	}
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
	return u.DeleteRoot()
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
	err3 := u.root.close()
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return err3
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

// populateOffSeqs reads the entries in the logs and builds User.offSeqs.
func (u *User) populateOffSeqs() {
	data := make([]byte, 4096)
	for _, file := range u.files {
		fd, err := os.Open(file.name)
		if err != nil {
			log.Error.Printf("dir/server/serverlog.populateOffSeqs: user %s: %v", u.name, err)
			return
		}
		defer fd.Close()
		offset := int64(0)
		for {
			var le Entry
			count, err := le.unmarshal(fd, data, offset)
			if err != nil {
				break
			}
			seq := le.Entry.Sequence
			if file.version == 0 {
				seq &= version0SeqMask
			}
			u.addOffSeq(file.offset+offset, seq)
			offset += int64(count)
		}
	}
}

func (u *User) setV1Transition() {
	if len(u.files) == 0 || u.files[0].version > 0 {
		return // No old logs.
	}
	// Read the first entry past the transition, looking for the first non-zero time.
	// It may take several files to get there.
	data := make([]byte, 4096)
	for _, file := range u.files {
		if file.version == 0 {
			continue
		}
		fd, err := os.Open(file.name)
		if err != nil {
			log.Error.Printf("dir/server/serverlog.setV1Transition: user %s: %v", u.name, err)
			return
		}
		defer fd.Close()
		offset := int64(0)
		for {
			var le Entry
			count, err := le.unmarshal(fd, data, offset)
			if err != nil {
				// EOF or otherwise, go to next file.
				break
			}
			offset += int64(count)
			if le.Entry.Time != 0 {
				u.v1Transition = le.Entry.Time
				return
			}
		}
	}
	// If there were any files but we got here
	// then the transition happens now.
	if len(u.files) > 0 {
		u.v1Transition = upspin.Now()
		return
	}
	// No luck. Zero it is. TODO: Should we fail?
}

// V1Transition returns a time that marks the transition from old (version 0)
// logs to version 1. DirEntries created before this time use the old Sequence
// number scheme, in which the upper 23 bits are noise. These should be
// cleared before reporting the sequence number to the client.
func (u *User) V1Transition() upspin.Time {
	return u.v1Transition
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

	next = offset
	count, err := le.unmarshal(r.fd, r.data[:], offset-r.file.offset)
	if err != nil {
		return le, next, err
	}
	next += int64(count)
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
	de, err := u.root.get()
	if err != nil {
		return nil, errors.E(u.Name(), err)
	}
	return de, nil
}

func (r *root) get() (*upspin.DirEntry, error) {
	buf, err := readAllFromTop(r.file)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	if len(buf) == 0 {
		return nil, errors.E(errors.NotExist, "no root for user")
	}
	var root upspin.DirEntry
	more, err := root.Unmarshal(buf)
	if err != nil {
		return nil, err
	}
	if len(more) != 0 {
		return nil, errors.E(errors.IO, errors.Errorf("root has %d left over bytes", len(more)))
	}
	r.savedSeq = root.Sequence
	return &root, nil
}

// SaveRoot saves the user's root entry to stable storage.
func (u *User) SaveRoot(root *upspin.DirEntry) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.root.put(root)
}

func (r *root) put(root *upspin.DirEntry) error {
	if r.readOnly {
		return errors.Str("cannot put read-only root")
	}
	if r.savedSeq == root.Sequence {
		return nil
	}

	buf, err := root.Marshal()
	if err != nil {
		return err
	}
	err = overwriteAndSync(r.file, buf)
	if err != nil {
		return err
	}
	r.savedSeq = root.Sequence

	// Store the root contents and tell
	// saveLoop to save it to the storage backend.
	r.mu.Lock()
	r.root = buf
	r.mu.Unlock()
	select {
	case r.saveRoot <- true:
	default:
	}

	return nil
}

// DeleteRoot deletes the root.
func (u *User) DeleteRoot() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.root.delete()
}

func (r *root) delete() error {
	if r.readOnly {
		return errors.Str("cannot delete read-only root")
	}
	if err := overwriteAndSync(r.file, []byte{}); err != nil {
		return err
	}
	// Don't delete the backup from storage, just in case.
	r.savedSeq = 0
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
		return 0, errors.E(errors.NotExist, cp.user.Name(), "no log offset for user")
	}
	offset, n := binary.Varint(buf)
	if n <= 0 {
		return 0, errors.E(errors.IO, "invalid offset read")
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
		return errors.E(errors.Invalid, "negative offset")
	}
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], offset)

	cp.user.mu.Lock()
	defer cp.user.mu.Unlock()

	return overwriteAndSync(cp.checkpointFile, tmp[:n])
}

// close closes the checkpoint. user.mu must be held
func (cp *checkpoint) close() error {
	if cp.checkpointFile != nil {
		err := cp.checkpointFile.Close()
		cp.checkpointFile = nil
		return err
	}
	return nil
}

// marshal packs the Entry into a new byte slice for storage.
func (le *Entry) marshal() ([]byte, error) {
	var b []byte
	// For historical reasons, the entry was written with binary.PutVarint,
	// but that adds unnecessary overhead.
	switch le.Op {
	case Put:
		b = append(b, 0x00)
	case Delete:
		b = append(b, 0x02)
	default:
		panic("bad Op in marshal")
	}

	entry, err := le.Entry.Marshal()
	if err != nil {
		return nil, err
	}
	b = appendBytes(b, entry)
	chksum := checksum(b)
	b = append(b, chksum[:]...)
	return b, nil
}

var checksumSalt = [4]byte{0xde, 0xad, 0xbe, 0xef}

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

// unmarshal unpacks a marshaled Entry from a Reader and stores it in the
// receiver. The data buffer is passed in so the routine can use it to do I/O
// and avoid allocating one itself. It must have at least 8 bytes, preferably
// more.
func (le *Entry) unmarshal(fd io.ReaderAt, data []byte, offset int64) (int, error) {
	// With a varint and a valid user name and so on, we will have at least 8 bytes.
	// It's coming from a file system, so we don't need to worry about partial reads.
	// If the incoming buffer is big enough, we'll get it all this round.
	// At least from the test, which uses bytes.Reader, we could get err==io.EOF
	// but still have some data.
	nRead, err := fd.ReadAt(data, offset)
	if err != nil && err != io.EOF || nRead < 8 { // Sanity check.
		return 0, errors.E(errors.IO, errors.Errorf("reading op: %s", err))
	}
	switch data[0] {
	case 0x00:
		le.Op = Put
	case 0x02:
		le.Op = Delete
	default:
		return 0, errors.E(errors.Invalid, errors.Errorf("unknown Op %d", data[0]))
	}

	size, n := binary.Varint(data[1:])
	if n <= 0 {
		return 0, errors.E(errors.IO, errors.Errorf("could not read entry"))
	}

	const reasonableEntrySize = 1 << 26 // 64MB
	if size <= 0 {
		return 0, errors.E(errors.IO, errors.Errorf("invalid entry size: %d", size))
	}
	if size > reasonableEntrySize {
		return 0, errors.E(errors.IO, errors.Errorf("entry size too large: %d", size))
	}
	entrySize := int(size) // Will not overflow.
	// We need a total of 1 + n + entrySize bytes, plus 4 bytes for the checksum,
	// which will give us a header, a marshaled entry, and a checksum.
	// Do we need to do another read?
	totalSize := 1 + n + entrySize + 4
	if totalSize > cap(data) {
		nData := make([]byte, totalSize)
		copy(nData, data)
		data = nData
	}
	data = data[:totalSize]
	if totalSize > nRead {
		n, err := fd.ReadAt(data[nRead:], offset+int64(nRead))
		if err != nil && err != io.EOF { // We'll check the count below.
			return 0, errors.E(errors.IO, errors.Errorf("reading %d bytes from entry: got %d: %s", totalSize-nRead, n, err))
		}
		if n != totalSize-nRead {
			return 0, errors.E(errors.IO, errors.Errorf("incomplete read getting %d bytes from entry: got %d", totalSize-nRead, n))
		}
	}

	// Everything's loaded, so unpack it.
	body := data[1+n : len(data)-4]
	checksumData := data[len(data)-4:]
	leftOver, err := le.Entry.Unmarshal(body)
	if err != nil {
		return 0, errors.E(errors.IO, err)
	}
	if len(leftOver) != 0 {
		return 0, errors.E(errors.IO, errors.Errorf("%d bytes left; log misaligned for entry %+v", len(leftOver), le.Entry))
	}
	chksum := checksum(data[:len(data)-4]) // Everything but the checksum bytes.
	for i, c := range chksum {
		if c != checksumData[i] {
			return 0, errors.E(errors.IO, errors.Errorf("invalid checksum: got %x, expected %x for entry %+v", chksum, checksumData, le.Entry))
		}
	}
	return len(data), nil
}
