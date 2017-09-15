// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serverlog maintains logs for directory servers, permitting
// replay, recovering, and mirroring.
package serverlog

// This file defines and implements three components for record keeping for a
// Tree:
//
// 1) Writer - writes log entries to the end of the log file.
// 2) Reader - reads log entries from any offset of the log file.
// 3) Index - saves the most recent commit point in the log and the root.
//
// The structure on disk is, relative to a log directory:
//
// tree.root.<username>  - root entry for username
// tree.index.<username> - log index for username
// d.tree.log.<username> - subdirectory for username, containing files named:
// <offset> - log greater than offset but less than the next offset file.
//
// There is also a legacy file tree.log.<username> which is hard linked to
// offset zero in the d.tree.log.<username> directory.
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

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

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

// Writer is an append-only log of Entry.
type Writer struct {
	user upspin.UserName // user for whom this log is intended.

	mu         sync.Mutex // protects fields below.
	file       *os.File   // file descriptor for the log.
	fileOffset int64      // offset of the first record from the file.
}

// Write implements io.Writer for the our Writer type.
// It is the method clients use to append data to the set of log files.
func (w *Writer) Write(b []byte) (int, error) {
	return w.file.Write(b)
}

// Reader reads LogEntries from the log.
type Reader struct {
	// writer is the Writer who owns the log.
	writer *Writer

	// mu protects the fields below. If wmu must be held, it must be
	// held after mu.
	mu sync.Mutex

	// fileOffset is the offset of the first record from the file we're
	// reading now.
	fileOffset int64

	// file is the file for the current log, indicated by fileOffset.
	file *os.File

	// offsets is a descending list of the known offsets available for
	// reading.
	offsets []int64
}

// Index reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress
// processing the log and storing the root.
type Index struct {
	user upspin.UserName // user for whom this logindex is intended.

	mu        *sync.Mutex // protects the files, making reads/write atomic.
	indexFile *os.File    // file descriptor for the last index in the log.
	rootFile  *os.File    // file descriptor for the root of the tree.
}

const oldStyleLogFilePrefix = "tree.log."

// New returns a new Writer log and a new Index for a user, logging to
// and from a given directory accessible to the local file system. If directory
// already contains a log or a log index for the user they are opened and
// returned. Otherwise they are created.
//
// Only one Writer per user can be opened in a directory or unpredictable
// results may occur.
func New(user upspin.UserName, directory string) (*Writer, *Index, error) {
	subdir := logSubDir(user, directory) // user's sub directory.

	// Make the log directory if it doesn't exist.
	// (MkdirAll returns a nil error if the directory exists.)
	if err := os.MkdirAll(subdir, 0700); err != nil {
		return nil, nil, errors.E(errors.IO, err)
	}

	off := logOffsetsFor(subdir)
	if off[0] == 0 { // Possibly starting a new log.
		// Is there an existing, old-style log file? If so, hard link it
		// to the zero offset entry in the user's subdirectory.
		// TODO: remove at some point. Must warn stragglers to first
		// patch their systems with this change before we remove it.
		oldLogName := filepath.Join(directory, oldStyleLogFilePrefix+string(user))
		newLogName := logFile(user, 0, directory)
		err := linkIfNotExist(oldLogName, newLogName)
		if err != nil {
			return nil, nil, errors.E(errors.IO, err)
		}
	}

	loc := logFile(user, off[0], directory)
	loggerFile, err := os.OpenFile(loc, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(errors.IO, err)
	}

	// We now have a new log name. Ensure we create an old log name too (for
	// new roots) so that we could go back to old naming style if needed.
	if off[0] == 0 {
		oldLogName := filepath.Join(directory, oldStyleLogFilePrefix+string(user))
		newLogName := logFile(user, 0, directory)
		err := linkIfNotExist(newLogName, oldLogName)
		if err != nil {
			return nil, nil, errors.E(errors.IO, err)
		}
	}

	l := &Writer{
		user:       user,
		file:       loggerFile,
		fileOffset: off[0],
	}

	rloc := rootFile(user, directory)
	iloc := indexFile(user, directory)
	rootFile, err := os.OpenFile(rloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(errors.IO, err)
	}
	indexFile, err := os.OpenFile(iloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(errors.IO, err)
	}
	li := &Index{
		user:      user,
		mu:        &sync.Mutex{},
		indexFile: indexFile,
		rootFile:  rootFile,
	}
	return l, li, nil
}

// linkIfNotExist links oldname to newname if newname does not yet exist.
// Otherwise it does nothing. If oldname does not exist, it does nothing.
func linkIfNotExist(oldname, newname string) error {
	_, err := os.Stat(newname)
	if err == nil {
		// Already exist, nothing to do.
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err = os.Stat(oldname)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.Link(oldname, newname)
}

// HasLog reports whether user has logs in directory.
func HasLog(user upspin.UserName, directory string) (bool, error) {
	var firstErr error
	for _, name := range []string{
		filepath.Join(directory, oldStyleLogFilePrefix+string(user)),
		logSubDir(user, directory),
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

// DeleteLogs deletes all data for a user in directory. Any existing logs
// associated with user must not be used subsequently.
func DeleteLogs(user upspin.UserName, directory string) error {
	for _, fn := range []string{
		filepath.Join(directory, oldStyleLogFilePrefix+string(user)),
		rootFile(user, directory),
		indexFile(user, directory),
	} {
		err := os.Remove(fn)
		if err != nil && !os.IsNotExist(err) {
			return errors.E(errors.IO, err)
		}
	}
	// Remove the user's log directory, if any, with all its contents.
	// Note: RemoveAll returns nil if the subdir does not exist.
	err := os.RemoveAll(logSubDir(user, directory))
	if err != nil && !os.IsNotExist(err) {
		return errors.E(errors.IO, err)
	}
	return nil
}

// userGlob returns the set of users in the directory that match the pattern.
// The pattern is as per filePath.Glob, applied to the directory.
func userGlob(pattern string, directory string) ([]upspin.UserName, error) {
	prefix := rootFile("", directory)
	matches, err := filepath.Glob(rootFile(upspin.UserName(pattern), directory))
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

func logFile(user upspin.UserName, offset int64, directory string) string {
	return filepath.Join(logSubDir(user, directory), fmt.Sprintf("%d", offset))
}

func logSubDir(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "d.tree.log."+string(user))
}

func indexFile(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "tree.index."+string(user))
}

func rootFile(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "tree.root."+string(user))
}

// logOffsetsFor returns in descending order a list of log offsets in a log
// directory for a user.
// If no log directory exists, the only offset returned is 0.
func logOffsetsFor(directory string) []int64 {
	offs, err := filepath.Glob(filepath.Join(directory, "*"))
	if err != nil || len(offs) == 0 {
		return []int64{0}
	}
	var offsets []int64
	for _, o := range offs {
		off, err := strconv.ParseInt(filepath.Base(o), 10, 64)
		if err != nil {
			log.Error.Printf("dir/server/tree.logOffsetsFor: Can't parse log offset: %s", o)
			continue
		}
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] > offsets[j] })
	return offsets
}

// User returns the user name who owns the root of the tree that this log represents.
func (w *Writer) User() upspin.UserName {
	return w.user
}

// Append appends a Entry to the end of the log.
func (w *Writer) Append(e *Entry) error {
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	prevOffs := lastOffset(w.file)

	// Is it time to move to a new log file?
	if prevOffs >= MaxLogSize {
		dir := filepath.Dir(w.file.Name())
		// Close the current underlying log file.
		err = w.close()
		if err != nil {
			return errors.E(errors.IO, err)
		}
		// Create a new log file where the previous one left off.
		w.fileOffset += prevOffs
		loc := filepath.Join(dir, fmt.Sprintf("%d", w.fileOffset))
		w.file, err = os.OpenFile(loc, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return errors.E(errors.IO, err)
		}
		prevOffs = 0
	}

	// File is append-only, so this is guaranteed to write to the tail.
	n, err := w.file.Write(buf)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	err = w.file.Sync()
	if err != nil {
		return errors.E(errors.IO, err)
	}
	// Sanity check: flush worked and the new offset relative to the
	// beginning of this file is the expected one.
	newOffs := prevOffs + int64(n)
	if newOffs != lastOffset(w.file) {
		// This might indicate a race somewhere, despite the locks.
		return errors.E(errors.IO, errors.Errorf("file.Sync did not update offset: expected %d, got %d", newOffs, lastOffset(w.file)))
	}
	return nil
}

// ReadAt reads an entry from the log at offset. It returns the log entry and
// the next offset.
func (r *Reader) ReadAt(offset int64) (le Entry, next int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The maximum offset we can satisfy with the current log file.
	maxOff := r.fileOffset + lastOffset(r.file)

	// Is the requested offset outside the bounds of the current log file?
	before := offset < r.fileOffset
	after := offset >= maxOff
	if before || after {
		if after {
			// Load new file offsets in case there was a log rotation.
			r.offsets = logOffsetsFor(filepath.Dir(r.file.Name()))
		}
		readOffset := r.readOffet(offset)
		// Locate the file and open it.
		err := r.openLogAtOffset(readOffset, filepath.Dir(r.file.Name()))
		if err != nil {
			return le, 0, errors.E(errors.IO, err)
		}
		// Recompute maxOff for the new file.
		maxOff = r.fileOffset + lastOffset(r.file)
	}

	// If we're reading from the tail file (max r.readOffsets), then we
	// must lock wmu to avoid reading partially-written data.
	if r.offsets[0] == r.fileOffset {
		r.writer.mu.Lock()
		defer r.writer.mu.Unlock()
	}

	// Are we past the end of the current file?
	if offset >= maxOff {
		return le, maxOff, nil
	}

	_, err = r.file.Seek(offset-r.fileOffset, io.SeekStart)
	if err != nil {
		return le, 0, errors.E(errors.IO, err)
	}
	next = offset
	checker := newChecker(r.file)
	defer checker.close()

	err = le.unmarshal(checker)
	if err != nil {
		return le, next, err
	}
	next = next + int64(checker.count)
	return
}

// readOffset returns the log we must read from to satisfy offset. If offset
// is not in the range of what we have stored it returns -1.
func (r *Reader) readOffet(offset int64) int64 {
	for _, o := range r.offsets { // r.offsets are in descending order.
		if offset >= o {
			return o
		}
	}
	return -1
}

// LastOffset returns the offset of the end of the file or -1 on error.
func (w *Writer) LastOffset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fileOffset + lastOffset(w.file)
}

// LastOffset returns the offset of the end of the file or -1 on error.
func (r *Reader) LastOffset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If we're reading from the same file as the current Writer, lock it.
	// Order is important.
	if r.fileOffset == r.offsets[0] {
		r.writer.mu.Lock()
		defer r.writer.mu.Unlock()
	}

	return r.fileOffset + lastOffset(r.file)
}

// lastOffset returns the offset of the end of the file or -1 on error.
// The file must be changed simultaneously with this call.
func lastOffset(f *os.File) int64 {
	fi, err := f.Stat()
	if err != nil {
		return -1
	}
	return fi.Size()
}

// Truncate truncates the log at offset.
func (w *Writer) Truncate(offset int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Are we truncating the tail file?
	if offset >= w.fileOffset {
		err := w.file.Truncate(w.fileOffset - offset)
		if err != nil {
			return err
		}
		return nil
	}

	// Otherwise, locate the existing offsets and delete everything from
	// this point on.
	base := filepath.Dir(w.file.Name())
	offsets := logOffsetsFor(base)

	err := w.close()
	if err != nil {
		return errors.E(errors.IO, err)
	}

	var i int
	for i = 0; i < len(offsets) && offsets[i] >= offset; i++ {
		os.Remove(filepath.Join(base, fmt.Sprintf("%d", offsets[i])))
	}

	loc := filepath.Join(base, fmt.Sprintf("%d", offsets[i]))
	w.file, err = os.OpenFile(loc, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	w.fileOffset = offsets[i]
	err = w.file.Truncate(offset - w.fileOffset)
	if err != nil {
		return err
	}
	return nil
}

// NewReader makes a reader of the log.
func (w *Writer) NewReader() (*Reader, error) {
	r := &Reader{}

	// Order is important.
	r.mu.Lock()
	defer r.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	r.writer = w
	r.offsets = logOffsetsFor(filepath.Dir(w.file.Name()))

	dir := filepath.Dir(w.file.Name())
	err := r.openLogAtOffset(w.fileOffset, dir)
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	return r, nil
}

// openLogAtOffset opens the log file relative to a directory where the absolute
// offset is stored and sets it as this Reader's current file.
// r.mu must be held.
func (r *Reader) openLogAtOffset(offset int64, directory string) error {
	fname := filepath.Join(directory, fmt.Sprintf("%d", offset))
	// Re-opening the same offset?
	if r.file != nil && r.file.Name() == fname {
		r.fileOffset = offset
		return nil
	}
	f, err := os.Open(fname)
	if err != nil {
		return err
	}
	if r.file != nil {
		r.file.Close()
	}
	r.file = f
	r.fileOffset = offset
	return nil
}

// Close closes the writer.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.close()
}

// close closes the writer. w.mu must be held.
func (w *Writer) close() error {
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Close closes the reader.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file != nil {
		err := r.file.Close()
		r.file = nil
		return err
	}
	return nil
}

// User returns the user name who owns the root of the tree that this
// log index represents.
func (li *Index) User() upspin.UserName {
	return li.user
}

// Root returns the user's root by retrieving it from local stable storage.
func (li *Index) Root() (*upspin.DirEntry, error) {
	li.mu.Lock()
	defer li.mu.Unlock()

	var root upspin.DirEntry
	buf, err := readAllFromTop(li.rootFile)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, errors.E(errors.NotExist, li.user, errors.Str("no root for user"))
	}
	more, err := root.Unmarshal(buf)
	if err != nil {
		return nil, err
	}
	if len(more) != 0 {
		return nil, errors.E(errors.IO, errors.Errorf("root has %d left over bytes", len(more)))
	}
	return &root, nil
}

// SaveRoot saves the user's root entry to stable storage.
func (li *Index) SaveRoot(root *upspin.DirEntry) error {
	buf, err := root.Marshal()
	if err != nil {
		return err
	}

	li.mu.Lock()
	defer li.mu.Unlock()
	return overwriteAndSync(li.rootFile, buf)
}

// DeleteRoot deletes the root.
func (li *Index) DeleteRoot() error {
	li.mu.Lock()
	defer li.mu.Unlock()

	return overwriteAndSync(li.rootFile, []byte{})
}

// Clone makes a read-only copy of the log index.
func (li *Index) Clone() (*Index, error) {
	li.mu.Lock()
	defer li.mu.Unlock()

	idx, err := os.Open(li.indexFile.Name())
	if os.IsNotExist(err) {
		return nil, errors.E(errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	root, err := os.Open(li.rootFile.Name())
	if err != nil {
		return nil, errors.E(errors.IO, err)
	}
	newLog := *li
	newLog.indexFile = idx
	newLog.rootFile = root
	return &newLog, nil
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
func (li *Index) ReadOffset() (int64, error) {
	li.mu.Lock()
	defer li.mu.Unlock()

	buf, err := readAllFromTop(li.indexFile)
	if err != nil {
		return 0, errors.E(errors.IO, err)
	}
	if len(buf) == 0 {
		return 0, errors.E(errors.NotExist, li.user, errors.Str("no log offset for user"))
	}
	offset, n := binary.Varint(buf)
	if n <= 0 {
		return 0, errors.E(errors.IO, errors.Str("invalid offset read"))
	}
	return offset, nil
}

// SaveOffset saves to stable storage the offset to process next.
func (li *Index) SaveOffset(offset int64) error {
	if offset < 0 {
		return errors.E(errors.Invalid, errors.Str("negative offset"))
	}
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], offset)

	li.mu.Lock()
	defer li.mu.Unlock()

	return overwriteAndSync(li.indexFile, tmp[:n])
}

// Close closes the Index.
func (li *Index) Close() error {
	li.mu.Lock()
	defer li.mu.Unlock()

	var firstErr error
	if li.indexFile != nil {
		firstErr = li.indexFile.Close()
		li.indexFile = nil
	}
	if li.rootFile != nil {
		err := li.rootFile.Close()
		li.rootFile = nil
		if err != nil && firstErr == nil {
			firstErr = err
		}
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
