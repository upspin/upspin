// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

// This file defines and implements Log and LogIndex for use in Tree.

import (
	"bufio"
	"encoding/binary"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// Operation is the kind of operation performed on the DirEntry.
type Operation int

// Operations on dir entries that are logged.
const (
	Put Operation = iota
	Delete
)

// LogEntry is the unit of logging.
type LogEntry struct {
	Op    Operation
	Entry upspin.DirEntry
}

// Log represents the log of DirEntry changes. It is primarily used by
// Tree (provided through its Config struct) to log changes.
type Log struct {
	user upspin.UserName // user for whom this log is intended.

	mu   *sync.Mutex // protects the file, making reads/write atomic.
	file *os.File    // file descriptor for the log.
}

// LogIndex reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress
// processing the log and storing the root.
type LogIndex struct {
	user upspin.UserName // user for whom this logindex is intended.

	mu        *sync.Mutex // protects the files, making reads/write atomic.
	indexFile *os.File    // file descriptor for the last index in the log.
	rootFile  *os.File    // file descriptor for the root of the tree.
}

// NewLogs returns a new Log and a new LogIndex for a user, logging to and from
// a given directory accessible to the local file system. If directory already
// contains a Log and/or a LogIndex for the user they are opened and returned.
// Otherwise one is created.
//
// Only one pair of read-write Log and LogIndex for a user in the same
// directory can be opened. If two are opened and used simultaneously, results
// will be unpredictable. This limitation does not apply to read-only clones
// created by Clone.
func NewLogs(user upspin.UserName, directory string) (*Log, *LogIndex, error) {
	const op = "dir/server/tree.NewLogs"
	loc := logFile(user, directory)
	loggerFile, err := os.OpenFile(loc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(op, errors.IO, err)
	}
	l := &Log{
		user: user,
		mu:   &sync.Mutex{},
		file: loggerFile,
	}

	rloc := rootFile(user, directory)
	iloc := indexFile(user, directory)
	rootFile, err := os.OpenFile(rloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(op, errors.IO, err)
	}
	indexFile, err := os.OpenFile(iloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(op, errors.IO, err)
	}
	li := &LogIndex{
		user:      user,
		mu:        &sync.Mutex{},
		indexFile: indexFile,
		rootFile:  rootFile,
	}
	return l, li, nil
}

// HasLog reports whether user has logs in directory.
func HasLog(user upspin.UserName, directory string) (bool, error) {
	const op = "dir/server/tree.HasLog"
	loc := logFile(user, directory)
	loggerFile, err := os.OpenFile(loc, os.O_RDONLY, 0600)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errors.E(op, errors.IO, err)
	}
	loggerFile.Close()
	return true, nil
}

// DeleteLogs deletes all data for a user in directory. Any existing logs
// associated with user must not be used subsequently.
func DeleteLogs(user upspin.UserName, directory string) error {
	const op = "dir/server/tree.DeleteLogs"
	for _, fn := range []string{
		logFile(user, directory),
		rootFile(user, directory),
		indexFile(user, directory),
	} {
		err := os.Remove(fn)
		if err != nil {
			return errors.E(op, errors.IO, err)
		}
	}
	return nil
}

// ListUsers applies a pattern to all known users in directory and returns
// the matches. The format of the pattern is the same used by
// path/filepath.Match. For example, to list all users name with suffix a valid
// pattern could be "*+*@*".
func ListUsers(pattern string, directory string) ([]upspin.UserName, error) {
	const op = "dir/server/tree.GlobUsers"
	prefix := logFile("", directory)
	matches, err := filepath.Glob(logFile(upspin.UserName(pattern), directory))
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	users := make([]upspin.UserName, len(matches))
	for i, m := range matches {
		users[i] = upspin.UserName(strings.TrimPrefix(m, prefix))
	}
	return users, nil
}

func logFile(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "tree.log."+string(user))
}

func indexFile(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "tree.index."+string(user))
}

func rootFile(user upspin.UserName, directory string) string {
	return filepath.Join(directory, "tree.root."+string(user))
}

// User returns the user name who owns the root of the tree that this log represents.
func (l *Log) User() upspin.UserName {
	return l.user
}

// Append appends a LogEntry to the end of the log.
func (l *Log) Append(e *LogEntry) error {
	const op = "dir/server/tree.Log.Append"
	buf, err := e.marshal()
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	prevOffs, err := l.file.Seek(0, io.SeekEnd)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	n, err := l.file.Write(buf)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	err = l.file.Sync()
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	// Sanity check: flush worked and the new offset is the expected one.
	newOffs := prevOffs + int64(n)
	if newOffs != l.lastOffset() {
		// This might indicate a race somewhere, despite the locks.
		return errors.E(op, errors.IO, errors.Errorf("file.Sync did not update offset: expected %d, got %d", newOffs, l.lastOffset()))
	}
	return nil
}

// ReadAt reads a log entry from the log starting at offset. It
// returns the next offset.
func (l *Log) ReadAt(offset int64) (le LogEntry, next int64, err error) {
	const op = "dir/server/tree.Log.Read"
	l.mu.Lock()
	defer l.mu.Unlock()

	// We can't ReadAt here since unmarshal does the reading, typically one
	// byte at a time. So we Seek to the right position instead.
	fileOffset := l.lastOffset()
	if offset >= fileOffset {
		// End of file.
		return le, fileOffset, nil
	}
	_, err = l.file.Seek(offset, io.SeekStart)
	if err != nil {
		return le, 0, errors.E(op, errors.IO, err)
	}
	next = offset
	checker := newChecker(l.file)
	defer checker.close()
	err = le.unmarshal(checker)
	if err != nil {
		return le, 0, errors.E(op, errors.IO, err)
	}
	next = next + int64(checker.count)
	return
}

// LastOffset returns the offset of the end of the file or -1 on error.
func (l *Log) LastOffset() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastOffset()
}

// lastOffset returns the offset of the end of the file or -1 on error.
// l.mu must be held.
func (l *Log) lastOffset() int64 {
	fi, err := l.file.Stat()
	if err != nil {
		return -1
	}
	return fi.Size()
}

// Truncate truncates the log at offset.
func (l *Log) Truncate(offset int64) error {
	const op = "dir/server/tree.Log.Truncate"
	l.mu.Lock()
	defer l.mu.Unlock()

	err := l.file.Truncate(offset)
	if err != nil {
		return errors.E(op, err)
	}
	return nil
}

// Clone makes a read-only copy of the log.
func (l *Log) Clone() (*Log, error) {
	const op = "dir/server/tree.Log.Clone"
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.file.Name())
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	newLog := *l
	newLog.file = f
	return &newLog, nil
}

// Close closes the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

// User returns the user name who owns the root of the tree that this
// log index represents.
func (li *LogIndex) User() upspin.UserName {
	return li.user
}

// Root returns the user's root by retrieving it from local stable storage.
func (li *LogIndex) Root() (*upspin.DirEntry, error) {
	const op = "dir/server/tree.LogIndex.Root"
	li.mu.Lock()
	defer li.mu.Unlock()

	var root upspin.DirEntry
	buf, err := readAllFromTop(op, li.rootFile)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, errors.E(op, errors.NotExist, li.user, errors.Str("no root for user"))
	}
	more, err := root.Unmarshal(buf)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if len(more) != 0 {
		return nil, errors.E(op, errors.IO, errors.Errorf("root has %d left over bytes", len(more)))
	}
	return &root, nil
}

// SaveRoot saves the user's root entry to stable storage.
func (li *LogIndex) SaveRoot(root *upspin.DirEntry) error {
	const op = "dir/server/tree.LogIndex.SaveRoot"
	buf, err := root.Marshal()
	if err != nil {
		return errors.E(op, err)
	}

	li.mu.Lock()
	defer li.mu.Unlock()
	return overwriteAndSync(op, li.rootFile, buf)
}

// DeleteRoot deletes the root.
func (li *LogIndex) DeleteRoot() error {
	const op = "dir/server/tree.LogIndex.DeleteRoot"
	li.mu.Lock()
	defer li.mu.Unlock()

	return overwriteAndSync(op, li.rootFile, []byte{})
}

// Clone makes a read-only copy of the log index.
func (li *LogIndex) Clone() (*LogIndex, error) {
	const op = "dir/server/tree.LogIndex.Clone"
	li.mu.Lock()
	defer li.mu.Unlock()

	idx, err := os.Open(li.indexFile.Name())
	if os.IsNotExist(err) {
		return nil, errors.E(op, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	root, err := os.Open(li.rootFile.Name())
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	newLog := *li
	newLog.indexFile = idx
	newLog.rootFile = root
	return &newLog, nil
}

func overwriteAndSync(op string, f *os.File, buf []byte) error {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	n, err := f.Write(buf)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	err = f.Truncate(int64(n))
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	return f.Sync()
}

func readAllFromTop(op string, f *os.File) ([]byte, error) {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	return buf, nil
}

// ReadOffset reads from stable storage the offset saved by SaveOffset.
func (li *LogIndex) ReadOffset() (int64, error) {
	const op = "dir/server/tree.LogIndex.ReadOffset"
	li.mu.Lock()
	defer li.mu.Unlock()

	buf, err := readAllFromTop(op, li.indexFile)
	if err != nil {
		return 0, errors.E(op, errors.IO, err)
	}
	if len(buf) == 0 {
		return 0, errors.E(op, errors.NotExist, li.user, errors.Str("no log offset for user"))
	}
	offset, n := binary.Varint(buf)
	if n <= 0 {
		return 0, errors.E(op, errors.IO, errors.Str("invalid offset read"))
	}
	return offset, nil
}

// SaveOffset saves to stable storage the offset to process next.
func (li *LogIndex) SaveOffset(offset int64) error {
	const op = "dir/server/tree.LogIndex.SaveOffset"
	if offset < 0 {
		return errors.E(op, errors.Invalid, errors.Str("negative offset"))
	}
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], offset)

	li.mu.Lock()
	defer li.mu.Unlock()

	return overwriteAndSync(op, li.indexFile, tmp[:n])
}

// Close closes the LogIndex.
func (li *LogIndex) Close() error {
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

// marshal packs the LogEntry into a new byte slice for storage.
func (le *LogEntry) marshal() ([]byte, error) {
	const op = "dir/server/tree.LogEntry.marshal"
	var b []byte
	var tmp [16]byte // For use by PutVarint.
	// This should have been b = append(b, byte(le.Op)) since Operation
	// is known to fit in a byte. However, we already encode it with
	// Varint and changing it would cause backward-incompatible issues.
	n := binary.PutVarint(tmp[:], int64(le.Op))
	b = append(b, tmp[:n]...)

	entry, err := le.Entry.Marshal()
	if err != nil {
		return nil, errors.E(op, err)
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

// unmarshal unpacks a marshaled LogEntry from a Reader and stores it in the
// receiver.
func (le *LogEntry) unmarshal(r *checker) error {
	const op = "dir/server/tree.LogEntry.unmarshal"
	operation, err := binary.ReadVarint(r)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("reading op: %s", err))
	}
	le.Op = Operation(operation)
	entrySize, err := binary.ReadVarint(r)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("reading entry size: %s", err))
	}
	if entrySize <= 0 {
		return errors.E(op, errors.IO, errors.Errorf("invalid entry size: %d", entrySize))
	}
	// Read exactly entrySize bytes.
	data := make([]byte, entrySize)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("reading %d bytes from entry: %s", entrySize, err))
	}
	leftOver, err := le.Entry.Unmarshal(data)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	if len(leftOver) != 0 {
		return errors.E(op, errors.IO, errors.Errorf("%d bytes left; log misaligned for entry %+v", len(leftOver), le.Entry))
	}
	chk, err := r.readChecksum()
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("reading checksum: %s", err))
	}
	if chk != r.chksum {
		return errors.E(op, errors.IO, errors.Errorf("invalid checksum: got %x, expected %x for entry %+v", r.chksum, chk, le.Entry))
	}
	return nil
}
