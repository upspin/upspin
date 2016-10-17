// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

type copyState struct {
	state   *State
	flagSet *flag.FlagSet
	n       int
	verbose bool
}

func (c *copyState) logf(format string, args ...interface{}) {
	if c.verbose {
		log.Printf(format, args...)
	}
}

func (s *State) copyCommand(flagSet *flag.FlagSet, n int, verbose bool, src []string, dst string) {
	// TODO: Check for nugatory copies.
	// TODO: Globbing on all paths.
	cs := &copyState{
		state:   s,
		flagSet: flagSet,
		n:       n,
		verbose: verbose,
	}
	if s.isDir(dst) {
		s.copyToDir(cs, src, dst)
		return
	}
	if len(src) != 1 {
		s.failf("copying multiple files but %s is not a directory", dst)
		cs.flagSet.Usage()
	}
	s.copyToFile(cs, src[0], dst)
}

var notExist = errors.E(errors.NotExist)

// isDir reports whether the named item is a directory either in Upspin
// or in the local file system.
func (s *State) isDir(dir string) bool {
	// First we see if it's an Upspin name.
	_, err := path.Parse(upspin.PathName(dir))
	if err == nil {
		// It's a legal Upspin name. Is it a directory?
		entry, err := s.client.Lookup(upspin.PathName(dir), true)
		// Report the error here if it's anything odd, because otherwise
		// we'll report "not a directory" misleadingly.
		if err != nil && !errors.Match(notExist, err) {
			log.Printf("%q: %v", dir, err)
		}
		return err == nil && entry.IsDir()
	}
	// Not a legal Upspin name. Is it a local directory?
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// open opens the file regardless of its location.
func (s *State) open(file string) (io.ReadCloser, error) {
	if s.isDir(file) {
		return nil, errors.E(upspin.PathName(file), errors.IsDir)
	}
	parsed, err := path.Parse(upspin.PathName(file))
	if err == nil {
		return s.client.Open(parsed.Path())
	}
	return os.Open(file)
}

// create creates the file regardless of its location.
func (s *State) create(file string) (io.WriteCloser, error) {
	parsed, err := path.Parse(upspin.PathName(file))
	if err == nil {
		fd, err := s.client.Create(parsed.Path())
		return fd, err
	}
	fd, err := os.Create(file)
	return fd, err
}

// createInDir create file 'base' in directory 'dir' regardless of the
// directory's location.
func (s *State) createInDir(dir, base string) (string, io.WriteCloser, error) {
	parsed, err := path.Parse(upspin.PathName(dir))
	if err == nil {
		to := path.Join(parsed.Path(), base)
		fd, err := s.client.Create(to)
		return string(to), fd, err
	}
	to := filepath.Join(dir, base)
	fd, err := os.Create(filepath.Join(dir, base))
	return to, fd, err
}

// copyWork is the unit of work passed to the copy worker.
type copyWork struct {
	src    string
	reader io.ReadCloser
	dst    string
	writer io.WriteCloser
}

// copyToDir copies, in parallel, the source files ot the destination directory.
func (s *State) copyToDir(cs *copyState, src []string, dir string) {
	work := make(chan copyWork) // No need for buffering.
	var wait sync.WaitGroup

	// Deliver work to the workers.
	go func() {
		for _, from := range src {
			reader, err := s.open(from)
			if err != nil {
				s.fail(err)
				s.exitCode = 1
				continue
			}
			to, writer, err := s.createInDir(dir, filepath.Base(from))
			if err != nil {
				s.fail(err)
				s.exitCode = 1
				reader.Close()
				continue
			}
			work <- copyWork{
				src:    from,
				reader: reader,
				dst:    to,
				writer: writer,
			}
		}
		close(work)
	}()

	// Start the workers.
	wait.Add(cs.n)
	for i := 0; i < cs.n; i++ {
		go cs.worker(work, &wait)
	}

	wait.Wait()
}

// copyToFile copies the source to the destination.
func (s *State) copyToFile(cs *copyState, src, dst string) {
	reader, err := s.open(src)
	if err != nil {
		s.fail(err)
		s.exitCode = 1
		return
	}
	writer, err := s.create(dst)
	if err != nil {
		s.fail(err)
		s.exitCode = 1
		reader.Close()
		return
	}
	copy := copyWork{
		src:    src,
		reader: reader,
		dst:    dst,
		writer: writer,
	}
	cs.doCopy(copy)
}

func (cs *copyState) worker(work chan copyWork, wait *sync.WaitGroup) {
	defer wait.Done()
	for copy := range work {
		cs.doCopy(copy)
	}
}

func (cs *copyState) doCopy(copy copyWork) {
	cs.logf("start cp %s %s", copy.src, copy.dst)
	defer cs.logf("end cp %s %s", copy.src, copy.dst)
	defer copy.reader.Close()
	defer copy.writer.Close()
	_, err := io.Copy(copy.writer, copy.reader)
	if err != nil {
		cs.state.fail(err)
		return
	}
}
