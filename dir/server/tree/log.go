// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tree

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

// This file implements Log and LogIndex.

// logger implements Log. It's called logger because log is a package used
// by tree. It is not thread-safe.
type logger struct {
	user   upspin.UserName
	file   *os.File
	offset uint64
}

// logIndex implements LogIndex. It is not thread-safe.
type logIndex struct {
	user      upspin.UserName
	indexFile *os.File
	rootFile  *os.File
}

// NewLogs returns a new Log and a new LogIndex for a user, logging to and from
// a given directory accessible to the local file system. If directory already
// contains a log and/or a logindex for the user they are opened and returned.
// Otherwise one is created.
//
// Only one Log and LogIndex for a user in the same directory can be opened.
// If two are opened and used simultaneously, results will be unpredictable.
// Neither Log or LogIndex are thread-safe.
func NewLogs(user upspin.UserName, directory string) (Log, LogIndex, error) {
	const NewLogs = "Tree.NewLogs"
	loc := filepath.Join(directory, fmt.Sprintf("tree.log.%s", user))
	loggerFile, err := os.OpenFile(loc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(NewLogs, errors.IO, err)
	}
	offset, err := loggerFile.Seek(0, 2) // Seek to the end of file.
	if err != nil {
		return nil, nil, errors.E(NewLogs, errors.IO, err)
	}
	l := &logger{
		user:   user,
		file:   loggerFile,
		offset: uint64(offset),
	}

	rloc := filepath.Join(directory, fmt.Sprintf("tree.root.%s", user))
	iloc := filepath.Join(directory, fmt.Sprintf("tree.index.%s", user))
	rootFile, err := os.OpenFile(rloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(NewLogs, errors.IO, err)
	}
	indexFile, err := os.OpenFile(iloc, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, errors.E(NewLogs, errors.IO, err)
	}
	li := &logIndex{
		user:      user,
		indexFile: indexFile,
		rootFile:  rootFile,
	}
	return l, li, nil
}

// User implements Log.
func (l *logger) User() upspin.UserName {
	return l.user
}

// Append implements Log.
func (l *logger) Append(e *LogEntry) error {
	const Append = "Log.Append"
	buf, err := e.marshal()
	if err != nil {
		return err
	}
	offs, err := l.file.Seek(0, 2) // Seek to the end.
	if err != nil {
		return errors.E(Append, errors.IO, err)
	}
	n, err := l.file.Write(buf)
	if err != nil {
		return errors.E(Append, errors.IO, err)
	}
	// n is == len(buf) when err != nil, so no need to check.
	l.offset = uint64(offs) + uint64(n)
	return nil
}

// Read implements Log.
func (l *logger) Read(offset uint64, n int) (dst []LogEntry, next uint64, err error) {
	const Read = "Log.Read"
	if offset >= l.offset {
		// End of file.
		return dst, l.offset, nil
	}
	log.Debug.Printf("%s: seeking to offset %d, reading %d log entries", Read, offset, n)
	_, err = l.file.Seek(int64(offset), 0)
	if err != nil {
		return nil, 0, errors.E(Read, errors.IO, err)
	}
	next = uint64(offset)
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
		next = offset + uint64(cbr.count)
	}
	return
}

// LastOffset implements Log.
func (l *logger) LastOffset() uint64 {
	return l.offset
}

// User implements LogIndex.
func (li *logIndex) User() upspin.UserName {
	return li.user
}

// Root implements LogIndex.
func (li *logIndex) Root() (*upspin.DirEntry, error) {
	const Root = "LogIndex.Root"
	var root upspin.DirEntry
	buf, err := readAllFromTop(Root, li.rootFile)
	if err != nil {
		return nil, err
	}
	more, err := root.Unmarshal(buf)
	if err != nil {
		return nil, errors.E(Root, err)
	}
	if len(more) != 0 {
		return nil, errors.E(Root, errors.IO, errors.Errorf("root has %d left over bytes", len(more)))
	}
	return &root, nil
}

// SaveRoot implements LogIndex.
func (li *logIndex) SaveRoot(root *upspin.DirEntry) error {
	const SaveRoot = "LogIndex.SaveRoot"
	buf, err := root.Marshal()
	if err != nil {
		return errors.E(SaveRoot, err)
	}
	return overwriteAndSync(SaveRoot, li.rootFile, buf)
}

func overwriteAndSync(op string, f *os.File, buf []byte) error {
	_, err := f.Seek(0, 0) // Seek to the beginning
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
	_, err := f.Seek(0, 0) // Seek to the beginning
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	return buf, nil
}

// ReadOffset implements LogIndex.
func (li *logIndex) ReadOffset() (uint64, error) {
	const ReadOffset = "LogIndex.ReadOffset"
	buf, err := readAllFromTop(ReadOffset, li.indexFile)
	if err != nil {
		return 0, errors.E(ReadOffset, errors.IO, err)
	}
	offset, n := binary.Uvarint(buf)
	if n <= 0 {
		return 0, errors.E(ReadOffset, errors.IO, errors.Str("invalid offset read"))
	}
	return offset, nil
}

// SaveOffset implements LogIndex.
func (li *logIndex) SaveOffset(offset uint64) error {
	const SaveOffset = "LogIndex.SaveOffset"
	var tmp [16]byte // For use by PutUvarint.
	n := binary.PutUvarint(tmp[:], offset)
	return overwriteAndSync(SaveOffset, li.indexFile, tmp[:n])
}

// marshal packs the LogEntry nto a new byte slice for storage.
func (le *LogEntry) marshal() ([]byte, error) {
	const Marshal = "LogEntry.marshal"
	var b []byte
	var tmp [1]byte // For use by PutVarint.
	n := binary.PutVarint(tmp[:], int64(le.Op))
	b = append(b, tmp[:n]...)
	entry, err := le.Entry.Marshal()
	if err != nil {
		return nil, errors.E(Marshal, err)
	}
	b = appendBytes(b, entry)
	return b, nil
}

func appendBytes(b, bytes []byte) []byte {
	var tmp [16]byte // For use by PutUvarint.
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
	const Unmarshal = "LogEntry.unmarshal"
	op, err := binary.ReadVarint(r)
	if err != nil {
		return errors.E(Unmarshal, errors.IO, errors.Errorf("reading op: %s", err))
	}
	le.Op = Operation(op)
	entrySize, err := binary.ReadVarint(r)
	// entrySize has the size of the entry. Read it into memory now.
	data := make([]byte, entrySize)
	_, err = r.Read(data)
	if err != nil {
		return errors.E(Unmarshal, errors.IO, errors.Errorf("reading %d bytes from entry: %s", entrySize, err))
	}
	leftOver, err := le.Entry.Unmarshal(data)
	if err != nil {
		return errors.E(Unmarshal, err)
	}
	if len(leftOver) != 0 {
		return errors.E(Unmarshal, errors.IO, errors.Errorf("%d bytes left; log misaligned", len(leftOver)))
	}
	return nil
}
