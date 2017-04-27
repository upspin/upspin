// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircache

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"upspin.io/cache"
	"upspin.io/upspin"
)

// DumpLog writes human readable logs to stdout.
func DumpLog(cfg upspin.Config, dir string) error {
	const op = "rpc/dircache.dumpLog"

	l := &clog{
		cfg: cfg,
		dir: dir,
		lru: cache.NewLRU(LRUMax),
	}
	l.proxied = newProxiedDirs(l)

	// Dump the log files in ascending time order.
	files, highestLogFile, err := listSorted(dir, true)
	if err != nil {
		return err
	}
	l.highestLogFile = highestLogFile
	for _, lfi := range files {
		l.dumpLogFile(lfi.Name(l.dir))
	}

	return nil
}

// dumpLogFile writes a human readable log file to Stdout.
func (l *clog) dumpLogFile(fn string) error {
	// Open the log file.  If one didn't exist, just rename the new log file and return.
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	for {
		var e clogEntry
		if err := e.read(l, rd); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		fmt.Printf("%s\n", &e)
	}
	return nil
}
