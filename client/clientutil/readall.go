// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package clientutil implements common utilities shared by clients and those
// who act as clients, such as a DirServer being a client of a StoreServer.
package clientutil // import "upspin.io/client/clientutil"

import (
	"sync"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// ReadAll reads the entire contents of a DirEntry. The reader must have
// the necessary keys loaded in the config to unpack the cipher if the entry
// is encrypted.
func ReadAll(cfg upspin.Config, entry *upspin.DirEntry) ([]byte, error) {
	if entry.IsLink() {
		return nil, errors.E(entry.Name, errors.Invalid, "can't read a link entry")
	}
	if entry.IsIncomplete() {
		return nil, errors.E(entry.Name, errors.Permission)
	}
	if access.IsAccessFile(entry.SignedName) {
		// Access files must be written by their owners only.
		p, _ := path.Parse(entry.SignedName)
		if p.User() != entry.Writer {
			return nil, errors.E(errors.Invalid, p.User(), "writer of Access file does not match owner")
		}
	}

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.E(entry.Name, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(cfg, entry)
	if err != nil {
		return nil, errors.E(entry.Name, err) // Showstopper.
	}

	return readAll(cfg, bu, entry)
}

func readAll(cfg upspin.Config, bu upspin.BlockUnpacker, entry *upspin.DirEntry) ([]byte, error) {
	var data []byte

	done := make(chan error)
	concurrency := 16
	ciphers := make(chan nCipher)

	go func() {
		window := newSlidingWindow(concurrency)
		pos := 0

		for in := range ciphers {
			if in.err != nil {
				done <- in.err
				return
			}
			// Got a block that should be unpacked in the future.
			if in.blockNumber != pos {
				window.append(in)
				var found bool
				in, found = window.pop(pos)
				if !found {
					continue
				}
			}

			clear, err := bu.UnpackBlock(in.cipher, in.blockNumber)
			if err != nil {
				done <- errors.E(entry.Name, err)
				return
			}
			data = append(data, clear...)
			pos++
		}

		for window.len() > 0 {
			in, found := window.pop(pos)
			// TODO: I think should always be found.
			if !found {
				continue
			}

			clear, err := bu.UnpackBlock(in.cipher, in.blockNumber)
			if err != nil {
				done <- errors.E(entry.Name, err)
				return
			}
			data = append(data, clear...)
			pos++
		}

		close(done)
	}()

	sema := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for n := 0; ; n++ {
		block, ok := bu.NextBlock()
		if !ok {
			wg.Wait()
			close(ciphers)
			break // EOF
		}

		sema <- struct{}{}
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			defer func() { <-sema }()

			// block is known valid as per valid.DirEntry above.
			cipher, err := ReadLocation(cfg, block.Location)
			if err != nil {
				ciphers <- nCipher{blockNumber: n, cipher: nil, err: errors.E(err)}
				return
			}

			ciphers <- nCipher{blockNumber: n, cipher: cipher}
		}(n)
	}

	if err := <-done; err != nil {
		return nil, err
	}

	return data, nil
}

type slidingWindow struct {
	window []nCipher
}

func newSlidingWindow(cap int) *slidingWindow {
	return &slidingWindow{window: make([]nCipher, 0, cap)}
}

func (w *slidingWindow) len() int {
	return len(w.window)
}

func (w *slidingWindow) append(nc nCipher) {
	w.window = append(w.window, nc)
}

func (w *slidingWindow) pop(blockNumber int) (nc nCipher, found bool) {
	for i, val := range w.window {
		if val.blockNumber == blockNumber {
			last := len(w.window) - 1
			w.window[last], w.window[i] = w.window[i], w.window[last]
			w.window = w.window[:len(w.window)-1]
			return val, true
		}
	}

	return nCipher{}, false
}

type nCipher struct {
	blockNumber int
	cipher      []byte
	err         error
}

// ReadLocation uses the provided Config to fetch the contents of the given
// Location, following any StoreServer.Get redirects.
func ReadLocation(cfg upspin.Config, loc upspin.Location) ([]byte, error) {
	// firstError remembers the first error we saw.
	// If we fail completely we return it.
	var firstError error
	// isError reports whether err is non-nil and remembers it if it is.
	isError := func(err error) bool {
		if err == nil {
			return false
		}
		if firstError == nil {
			firstError = err
		}
		return true
	}

	// knownLocs stores the known Locations for this block. Value is
	// ignored.
	knownLocs := make(map[upspin.Location]bool)
	// Get the data for this block.
	// where is the list of locations to examine. It is updated in the loop.
	where := []upspin.Location{loc}
	for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
		loc := where[i]
		store, err := bind.StoreServer(cfg, loc.Endpoint)
		if isError(err) {
			continue
		}
		data, _, locs, err := store.Get(loc.Reference)
		if isError(err) {
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			return data, nil
		}
		// Add new locs to the list. Skip ones already there - they've been processed.
		for _, newLoc := range locs {
			if _, found := knownLocs[newLoc]; !found {
				where = append(where, newLoc)
				knownLocs[newLoc] = true
			}
		}
	}

	// If we arrive here, we have failed to find a block.
	if firstError != nil {
		return nil, errors.E(firstError)
	}
	return nil, errors.E(errors.IO, errors.Errorf("data for location %v not found on any store server", loc))
}
