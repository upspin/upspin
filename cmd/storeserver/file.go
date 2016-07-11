// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storeserver is a wrapper for a store implementation that presents it as a grpc interface.
// TODO: This file should live somewhere else; possibly in cloud/file.
package main

import (
	"io"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// BufferedChannelFile is an upspin.File that buffers a few bytes of data in a channel.
// Its main purpose is to serve as the glue between two streaming endpoints, one writing data and one reading it.
type BufferedChannelFile struct {
	ch             chan []byte
	closed         bool
	name           upspin.PathName
	blockSize      int
	readBuf        []byte
	readBufOffset  int
	writeBuf       []byte
	writeBufOffset int
}

var _ upspin.File = (*BufferedChannelFile)(nil)

// NewBufferedChannelFile creates a new bufferedChannelFile that expects reads and writes to operate on blockSize
// blocks. It will buffer up to 2*blockSize in memory.
func NewBufferedChannelFile(name upspin.PathName, blockSize int) *BufferedChannelFile {
	return &BufferedChannelFile{
		ch:        make(chan []byte, 1),
		closed:    false,
		name:      name,
		blockSize: blockSize,
		writeBuf:  make([]byte, blockSize),
	}
}

// Close implements upspin.File.
func (b *BufferedChannelFile) Close() error {
	// Whatever is in the write buffer must be flushed now.
	if b.writeBufOffset > 0 {
		b.ch <- b.writeBuf[:b.writeBufOffset]
	}
	b.closed = true
	close(b.ch)
	return nil
}

// Name implements upspin.File.
func (b *BufferedChannelFile) Name() upspin.PathName {
	return b.name
}

// Read implements upspin.File.
func (b *BufferedChannelFile) Read(buf []byte) (n int, err error) {
	// For reads, we don't check b.closed as readers can continue after the writer has closed it.

	// Position in buf where we're writing data.
	writeOffset := 0

	for {
		// Check if there's anything in our private read buffer already. If so, return what we can from it.
		remainingPrivate := len(b.readBuf) - b.readBufOffset
		if remainingPrivate > 0 {
			remainingSpaceInBuf := len(buf) - writeOffset
			// Can we read all that is in our read buffer?
			if remainingSpaceInBuf >= remainingPrivate {
				n := copy(buf[writeOffset:], b.readBuf[b.readBufOffset:])
				b.readBufOffset += n
				writeOffset += n
				if n == remainingSpaceInBuf {
					return writeOffset, nil
				}
				// Fall through to read more from the channel.
			} else {
				// Read partially and return.
				n := copy(buf[writeOffset:], b.readBuf[b.readBufOffset:b.readBufOffset+remainingSpaceInBuf])
				b.readBufOffset += n
				writeOffset += n
				return writeOffset, nil
			}
		}
		// Read from the channel.
		var ok bool
		b.readBuf, ok = <-b.ch
		if ok {
			b.readBufOffset = 0
		} else {
			// Channel closed. No more data.
			return writeOffset, io.EOF
		}
	}
}

// ReadAt implements upspin.File.
func (b *BufferedChannelFile) ReadAt(buf []byte, off int64) (n int, err error) {
	return 0, errors.E("ReadAt", errors.Syntax, errors.Str("ReadAt not implemented for BufferedChannelFile"))
}

// Write implements upspin.File.
func (b *BufferedChannelFile) Write(buf []byte) (n int, err error) {
	if b.closed {
		return 0, errors.E("Write", errors.IO, errors.Str("BufferedChannelFile is closed"))
	}

	// Position in buf where we're reading data from.
	readOffset := 0

	for {
		// Can we write to our private buffer still?
		remainingSpace := len(b.writeBuf) - b.writeBufOffset
		if remainingSpace <= 0 {
			// Never happens.
			panic("no remaining space?")
		}
		// Can we fill writeBuf with what's available in buf?
		remainingInBuf := len(buf) - readOffset
		if remainingInBuf == 0 {
			return readOffset, nil
		}
		if remainingInBuf >= remainingSpace {
			n := copy(b.writeBuf[b.writeBufOffset:], buf[readOffset:readOffset+remainingSpace])
			b.writeBufOffset += n
			readOffset += n
			// writeBuf should be full. Send it on channel.
			b.ch <- b.writeBuf[:b.writeBufOffset]
			b.writeBufOffset = 0
			b.writeBuf = make([]byte, b.blockSize) // Make the next write buffer
		} else {
			// Can't fill writeBuf. Buffer what we can.
			n := copy(b.writeBuf[b.writeBufOffset:], buf[readOffset:])
			readOffset += n
			b.writeBufOffset += n
			return readOffset, nil
		}
	}
}

// WriteAt implements upspin.File.
func (b *BufferedChannelFile) WriteAt(buf []byte, off int64) (n int, err error) {
	return 0, errors.E("WriteAt", errors.Syntax, errors.Str("WriteAt not implemented for BufferedChannelFile"))
}

// Seek implements upspin.File.
func (b *BufferedChannelFile) Seek(offset int64, whence int) (ret int64, err error) {
	return 0, errors.E("Seek", errors.Syntax, errors.Str("Seek not implemented for BufferedChannelFile"))
}
