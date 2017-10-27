// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// The dump program prints the contents of the logs in a given directory.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprint(os.Stderr, "usage: dump <dir>")
		flag.Usage()
		os.Exit(2)
	}
	data := make([]byte, 4096)
	for i, file := range findLogFiles(flag.Arg(0)) {
		fd, err := os.Open(file.name)
		if err != nil {
			log.Fatal(err)
		}
		defer fd.Close()
		offset := int64(0)
		for {
			var le Entry
			count, err := le.unmarshal(fd, data, offset)
			if err != nil {
				break
			}
			if file.version == 0 {
				le.Entry.Sequence &= version0SeqMask
			}
			fmt.Printf("%d: %q: op %s seq %d off %d\n", i, le.Entry.Name, le.Op, le.Entry.Sequence, offset+file.offset)
			offset += int64(count)
		}
	}
}

// Entry is the unit of logging.
type Entry struct {
	Op    Operation
	Entry upspin.DirEntry
}

const version0SeqMask = 1<<23 - 1

// Operation is the kind of operation performed on the DirEntry.
type Operation int

// Operations on dir entries that are logged.
const (
	Put Operation = iota
	Delete
)

func (op Operation) String() string {
	switch op {
	case Put:
		return "Put"
	case Delete:
		return "Del"
	}
	return fmt.Sprint(int(op))
}

type logFile struct {
	name    string // Full path name.
	index   int    // Position in User.files.
	version int    // Version number of the format used.
	offset  int64  // Offset at start of file.
}

func findLogFiles(dir string) []*logFile {
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		log.Fatal(err)
	}
	if len(files) == 0 {
		log.Fatalf("no log files in %s", dir)
	}
	var logFiles []*logFile
	for _, file := range files {
		// Format of name is ..../*tree.log.ann@example.com/oooo.vvvv where o=offset, v=version.
		// For old files, .vvvv will be missing, and version is 0.
		elems := strings.Split(filepath.Base(file), ".")
		var ints []int64
		fmt.Println(file, elems)
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
			index: len(logFiles),
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
		logFiles = append(logFiles, lf)
	}
	sort.Slice(logFiles, func(i, j int) bool { return logFiles[i].offset < logFiles[j].offset })
	return logFiles
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

var checksumSalt = [4]byte{0xde, 0xad, 0xbe, 0xef}

func checksum(buf []byte) [4]byte {
	var c [4]byte
	copy(c[:], checksumSalt[:])
	for i, b := range buf {
		c[i%4] ^= b
	}
	return c
}
