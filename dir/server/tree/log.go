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

// LogEntry is the unit of logging.
type LogEntry struct {
	Op    Operation
	Entry upspin.DirEntry
}

// Log represents the log of DirEntry changes. It is primarily used by
// Tree (provided through its Config struct) to log changes.
type Log struct {
	user   upspin.UserName // user for whom this log is intended.
	file   *os.File        // file descriptor for the log.
	offset int64           // last position appended to the log (end of log).
}

// LogIndex reads and writes from/to stable storage the log state information
// and the user's root entry. It is used by Tree to track its progress
// processing the log and storing the root.
type LogIndex struct {
	user      upspin.UserName // user for whom this logindex is intended.
	indexFile *os.File        // file descriptor for the last index in the log.
	rootFile  *os.File        // file descriptor for the root of the tree.
}

// NewLogs returns a new Log and a new LogIndex for a user, logging to and from
// a given directory accessible to the local file system. If directory already
// contains a Log and/or a LogIndex for the user they are opened and returned.
// Otherwise one is created.
//
// Only one Log and LogIndex for a user in the same directory can be opened.
// If two are opened and used simultaneously, results will be unpredictable.
func NewLogs(user upspin.UserName, directory string) (*Log, *LogIndex, error) {
	const op = "dir/server/tree.NewLogs"
	loc := filepath.Join(directory, "tree.log."+string(user))
	loggerFile, err := os.OpenFile(loc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(op, errors.IO, err)
	}
	offset, err := loggerFile.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, nil, errors.E(op, errors.IO, err)
	}
	l := &Log{
		user:   user,
		file:   loggerFile,
		offset: offset,
	}

	rloc := filepath.Join(directory, "tree.root."+string(user))
	iloc := filepath.Join(directory, "tree.index."+string(user))
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
		indexFile: indexFile,
		rootFile:  rootFile,
	}
	return l, li, nil
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
	offs, err := l.file.Seek(0, io.SeekEnd)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	n, err := l.file.Write(buf)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	// n is == len(buf) when err != nil, so no need to check.
	l.offset = offs + int64(n)
	return nil
}

// ReadAt reads at most n entries from the log starting at offset. It
// returns the next offset.
func (l *Log) ReadAt(n int, offset int64) (dst []LogEntry, next int64, err error) {
	const op = "dir/server/tree.Log.Read"
	if offset >= l.offset {
		// End of file.
		return dst, l.offset, nil
	}
	log.Debug.Printf("%s: seeking to offset %d, reading %d log entries", op, offset, n)
	_, err = l.file.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, 0, errors.E(op, errors.IO, err)
	}
	next = offset
	cbr := &countingByteReader{rd: bufio.NewReader(l.file)}
	for i := 0; i < n; i++ {
		var le LogEntry
		if next == l.offset {
			// End of file.
			return dst, l.offset, nil
		}
		err := le.unmarshal(cbr)
		if err != nil {
			return nil, 0, err
		}
		dst = append(dst, le)
		next = offset + int64(cbr.count)
	}
	return
}

// LastOffset returns the offset of the most-recently-appended entry or 0 if the
// log is empty.
func (l *Log) LastOffset() int64 {
	return l.offset
}

// Truncate truncates the log at offset.
func (l *Log) Truncate(offset int64) error {
	const op = "dir/server/tree.Log.Truncate"
	err := l.file.Truncate(offset)
	if err != nil {
		return errors.E(op, err)
	}
	l.offset = offset
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
	return overwriteAndSync(op, li.rootFile, buf)
}

// DeleteRoot deletes the root.
func (li *LogIndex) DeleteRoot() error {
	const op = "dir/server/tree.LogIndex.DeleteRoot"
	return overwriteAndSync(op, li.rootFile, []byte{})
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
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], offset)
	return overwriteAndSync(op, li.indexFile, tmp[:n])
}

// marshal packs the LogEntry into a new byte slice for storage.
func (le *LogEntry) marshal() ([]byte, error) {
	const op = "dir/server/tree.LogEntry.marshal"
	var b []byte
	var tmp [1]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(le.Op))
	b = append(b, tmp[:n]...)
	entry, err := le.Entry.Marshal()
	if err != nil {
		return nil, errors.E(op, err)
	}
	b = appendBytes(b, entry)
	return b, nil
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(len(bytes)))
	b = append(b, tmp[:n]...)
	b = append(b, bytes...)
	return b
}

// countingByteReader counts how many bytes are read by a bufio.Reader's
// ReadByte and Read methods.
type countingByteReader struct {
	rd    *bufio.Reader
	count int
}

// ReadByte implements io.ByteReader.
func (r *countingByteReader) ReadByte() (byte, error) {
	b, err := r.rd.ReadByte()
	if err == nil {
		r.count++
	}
	return b, err
}

// Read implements io.Reader.
func (r *countingByteReader) Read(p []byte) (n int, err error) {
	n, err = r.rd.Read(p)
	if err != nil {
		return
	}
	r.count += n
	return
}

// unmarshal unpacks a marshaled LogEntry from a Reader and stores it in the
// receiver.
func (le *LogEntry) unmarshal(r *countingByteReader) error {
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
	data := make([]byte, entrySize)
	_, err = r.Read(data)
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf("reading %d bytes from entry: %s", entrySize, err))
	}
	leftOver, err := le.Entry.Unmarshal(data)
	if err != nil {
		return errors.E(op, err)
	}
	if len(leftOver) != 0 {
		return errors.E(op, errors.IO, errors.Errorf("%d bytes left; log misaligned", len(leftOver)))
	}
	return nil
}
