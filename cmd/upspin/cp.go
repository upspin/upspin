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
	state      *State
	flagSet    *flag.FlagSet // Used only to call Usage.
	numWorkers int
	verbose    bool
}

func (c *copyState) logf(format string, args ...interface{}) {
	if c.verbose {
		log.Printf(format, args...)
	}
}

// A cpFile is a glob-expanded file name and an indication of whether
// it resides on Upspin.
type cpFile struct {
	path     string
	isUpspin bool
}

func (s *State) copyCommand(flagSet *flag.FlagSet, numWorkers int, verbose bool, src []string, dst string) {
	// TODO: Check for nugatory copies.
	cs := &copyState{
		state:      s,
		flagSet:    flagSet,
		numWorkers: numWorkers,
		verbose:    verbose,
	}
	// Glob the paths.
	var files []cpFile
	for _, file := range src {
		files = append(files, cs.glob(file)...)
	}
	files = append(files, cs.glob(dst)...)
	srcFiles, dstFile := files[:len(files)-1], files[len(files)-1] // We are guaranteed at least two entries in files.
	if s.isDir(dstFile) {
		s.copyToDir(cs, srcFiles, dstFile)
		return
	}
	if len(src) != 1 {
		s.failf("copying multiple files but %s is not a directory", dstFile.path)
		cs.flagSet.Usage()
	}
	s.copyToFile(cs, srcFiles[0], dstFile)
}

// isDir reports whether the file is a directory either in Upspin
// or in the local file system.
func (s *State) isDir(cf cpFile) bool {
	if cf.isUpspin {
		entry, err := s.client.Lookup(upspin.PathName(cf.path), true)
		// Report the error here if it's anything odd, because otherwise
		// we'll report "not a directory" misleadingly.
		if err != nil && !errors.Match(notExist, err) {
			log.Printf("%q: %v", cf.path, err)
		}
		return err == nil && entry.IsDir()
	}
	// Not an Upspin name. Is it a local directory?
	info, err := os.Stat(cf.path)
	return err == nil && info.IsDir()
}

var notExist = errors.E(errors.NotExist)

// open opens the file regardless of its location.
func (s *State) open(file cpFile) (io.ReadCloser, error) {
	if s.isDir(file) {
		return nil, errors.E(upspin.PathName(file.path), errors.IsDir)
	}
	if file.isUpspin {
		return s.client.Open(upspin.PathName(file.path))
	}
	return os.Open(file.path)
}

// create creates the file regardless of its location.
func (s *State) create(file cpFile) (io.WriteCloser, error) {
	if file.isUpspin {
		fd, err := s.client.Create(upspin.PathName(file.path))
		return fd, err
	}
	fd, err := os.Create(file.path)
	return fd, err
}

// createInDir create file 'base' in directory 'dir' regardless of the
// directory's location.
func (s *State) createInDir(dir cpFile, base string) (string, io.WriteCloser, error) {
	if dir.isUpspin {
		to := path.Join(upspin.PathName(dir.path), base)
		fd, err := s.client.Create(to)
		return string(to), fd, err
	}
	to := filepath.Join(dir.path, base)
	fd, err := os.Create(to)
	return to, fd, err
}

// copyWork is the unit of work passed to the copy worker.
type copyWork struct {
	src    string
	reader io.ReadCloser
	dst    string
	writer io.WriteCloser
}

// copyToDir copies, in parallel, the source files to the destination directory.
func (s *State) copyToDir(cs *copyState, src []cpFile, dir cpFile) {
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
			to, writer, err := s.createInDir(dir, filepath.Base(from.path))
			if err != nil {
				s.fail(err)
				s.exitCode = 1
				reader.Close()
				continue
			}
			work <- copyWork{
				src:    from.path,
				reader: reader,
				dst:    to,
				writer: writer,
			}
		}
		close(work)
	}()

	// Start the workers.
	wait.Add(cs.numWorkers)
	for i := 0; i < cs.numWorkers; i++ {
		go cs.worker(work, &wait)
	}

	wait.Wait()
}

// copyToFile copies the source to the destination.
func (s *State) copyToFile(cs *copyState, src, dst cpFile) {
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
		src:    src.path,
		reader: reader,
		dst:    dst.path,
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
	defer func() {
		cs.logf("end cp %s %s", copy.src, copy.dst)
		copy.reader.Close()
		err := copy.writer.Close()
		if err != nil {
			cs.state.fail(err)
		}
	}()
	_, err := io.Copy(copy.writer, copy.reader)
	if err != nil {
		cs.state.fail(err)
	}
}

// glob glob-expands the argument, which could be a local file
// name or an Upspin path name.
func (cs *copyState) glob(pattern string) (files []cpFile) {
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err == nil {
		// It's an Upspin path.
		for _, path := range cs.state.globUpspin(parsed.String()) {
			files = append(files, cpFile{
				path:     string(path),
				isUpspin: true,
			})
		}
		return files
	}
	// It's a local path.
	for _, path := range cs.state.globLocal(pattern) {
		files = append(files, cpFile{
			path:     path,
			isUpspin: false,
		})
	}
	return files
}
