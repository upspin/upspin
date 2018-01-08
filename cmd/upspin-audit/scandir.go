// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"upspin.io/path"
	"upspin.io/upspin"
)

// This file implements the directory scan. Because the network time of flight is
// significant to throughput, the scan is parallelized, which makes the code
// more intricate than we'd like.
// The code actually walks the directory tree using Glob. We could in principle
// use Watch(-1), but snapshots are problematic for Watch. We take care to
// avoid scanning a directory we've already seen, which Watch doesn't do on
// the server. Our code makes it practical to scan the snapshot tree.

const scanParallelism = 10 // Empirically chosen: speedup significant, not too many resources.

type dirScanner struct {
	State    *State
	inFlight sync.WaitGroup        // Count of directories we have seen but not yet processed.
	buffer   chan *upspin.DirEntry // Where to send directories for processing.
	dirsToDo chan *upspin.DirEntry // Receive from here to find next directory to process.
	done     chan *upspin.DirEntry // Send entries here once it is completely done, including children.
}

func (s *State) scanDirectories(args []string) {
	const help = `
Audit scan-dir scans the directory trees of the named user roots and produces a
list of store block references mentioned in those trees.

The list is written to files named "dir_EP_USER_TS" in the directory nominated
by -data, where "EP" is the store endpoint, "USER" is the directory user name,
and "TS" is the current time.

It should be run as a user that has full read access to the named roots.
`

	fs := flag.NewFlagSet("scan-dir", flag.ExitOnError)
	glob := fs.Bool("glob", true, "apply glob processing to the arguments")
	dataDir := dataDirFlag(fs)
	s.ParseFlags(fs, args, help, "audit scan-dir root ...")

	if fs.NArg() == 0 || fs.Arg(0) == "help" {
		fs.Usage()
		os.Exit(2)
	}

	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		s.Exit(err)
	}

	var paths []upspin.PathName
	if *glob {
		paths = s.GlobAllUpspinPath(fs.Args())
	} else {
		for _, p := range fs.Args() {
			paths = append(paths, upspin.PathName(p))
		}
	}

	// Check that the arguments are user roots.
	for _, p := range paths {
		parsed, err := path.Parse(p)
		if err != nil {
			s.Exit(err)
		}
		if !parsed.IsRoot() {
			s.Exitf("%q is not a user root", p)
		}
	}

	now := time.Now()

	sc := dirScanner{
		State:    s,
		buffer:   make(chan *upspin.DirEntry),
		dirsToDo: make(chan *upspin.DirEntry),
		done:     make(chan *upspin.DirEntry),
	}

	for i := 0; i < scanParallelism; i++ {
		go sc.dirWorker()
	}
	go sc.bufferLoop()

	// Prime the pump.
	for _, p := range paths {
		de, err := s.DirServer(p).Lookup(p)
		if err != nil {
			s.Exit(err)
		}
		sc.do(de)
	}

	// Shut down the process tree once nothing is in flight.
	go func() {
		sc.inFlight.Wait()
		close(sc.buffer)
		close(sc.done)
	}()

	// Receive and collect the data.
	endpoints := make(refsByEndpoint)
	users := make(map[upspin.UserName]refsByEndpoint)
	for de := range sc.done {
		p, err := path.Parse(de.Name)
		if err != nil {
			s.Fail(err)
			continue
		}
		userSize := users[p.User()]
		if userSize == nil {
			userSize = make(refsByEndpoint)
			users[p.User()] = userSize
		}
		for _, block := range de.Blocks {
			ep := block.Location.Endpoint
			endpoints.addRef(ep, block.Location.Reference, block.Size, p.Path())
			userSize.addRef(ep, block.Location.Reference, block.Size, p.Path())
		}
	}

	// Print a summary.
	total := int64(0)
	for ep, refs := range endpoints {
		sum := int64(0)
		for _, ri := range refs {
			sum += ri.Size
		}
		total += sum
		fmt.Printf("%s: %d bytes (%s) (%d references)\n", ep.NetAddr, sum, ByteSize(sum), len(refs))
	}
	if len(endpoints) > 1 {
		fmt.Printf("%d bytes total (%s)\n", total, ByteSize(total))
	}

	// Write the data to files, one for each user/endpoint combo.
	for u, size := range users {
		for ep, refs := range size {
			file := filepath.Join(*dataDir, fmt.Sprintf("%s%s_%s_%d", dirFilePrefix, ep.NetAddr, u, now.Unix()))
			s.writeItems(file, refs.slice())
		}
	}
}

// do processes a DirEntry. If it's a file, we deliver it to the done channel.
// Otherwise it's a directory and we buffer it for expansion.
func (sc *dirScanner) do(entry *upspin.DirEntry) {
	if !entry.IsDir() {
		sc.done <- entry
	} else {
		sc.inFlight.Add(1)
		sc.buffer <- entry
	}
}

// bufferLoop gathers work to do and distributes it to the workers. It acts as
// an itermediary buffering work to avoid deadlock; without this loop, workers
// would both send to and receive from the dirsToDo channel. Once nothing is
// pending or in flight, bufferLoop shuts down the processing network.
func (sc *dirScanner) bufferLoop() {
	defer close(sc.dirsToDo)
	entriesPending := make(map[*upspin.DirEntry]bool)
	seen := make(map[string]bool) // Eirectories we have seen, keyed by references within.
	buffer := sc.buffer
	var keyBuf bytes.Buffer // For creating keys for the seen map.
	for {
		var entry *upspin.DirEntry
		var dirsToDo chan *upspin.DirEntry
		if len(entriesPending) > 0 {
			// Pick one entry at random from the map.
			for entry = range entriesPending {
				break
			}
			dirsToDo = sc.dirsToDo
		} else if buffer == nil {
			return
		}
		select {
		case dirsToDo <- entry:
			delete(entriesPending, entry)
		case entry, active := <-buffer:
			if !active {
				buffer = nil
				break
			}
			// If this directory has already been done, don't do it again.
			// This situation arises when scanning a snapshot tree, as most of
			// the directories are just dups of those in the main tree.
			// We identify duplication by comparing the list of references within.
			// TODO: Find a less expensive check.
			keyBuf.Reset()
			for i := range entry.Blocks {
				b := &entry.Blocks[i]
				fmt.Fprintf(&keyBuf, "%q %q\n", b.Location.Endpoint, b.Location.Reference)
			}
			key := keyBuf.String()
			if seen[key] {
				sc.inFlight.Done()
			} else {
				seen[key] = true
				entriesPending[entry] = true
			}
		}
	}
}

// dirWorker receives DirEntries for directories from the dirsToDo channel
// and processes them, descending into their components and delivering
// the results to the buffer channel.
func (sc *dirScanner) dirWorker() {
	for dir := range sc.dirsToDo {
		des, err := sc.State.DirServer(dir.Name).Glob(upspin.AllFilesGlob(dir.Name))
		if err != nil {
			sc.State.Fail(err)
		} else {
			for _, de := range des {
				sc.do(de)
			}
		}
		sc.done <- dir
		sc.inFlight.Done()
	}
}
