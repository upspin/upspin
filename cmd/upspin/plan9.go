// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// findUpspinBinaries finds all the upspin-* binaries in $path.
// It may return the same name multiple times; the caller should
// filter. (But all it's going to do is sort and print them, so it's easy.)
func findUpspinBinaries() []string {
	path := os.Getenv("path")
	var cmds []string
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		fd, err := os.Open(dir)
		if err != nil {
			continue
		}
		files, err := fd.Readdir(0)
		fd.Close()
		if err != nil {
			continue
		}
		for _, info := range files {
			if !strings.HasPrefix(info.Name(), "upspin-") {
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if info.Mode().Perm()&0100 == 0 {
				continue
			}
			cmds = append(cmds, info.Name()[len("upspin-"):])
		}
	}
	return cmds
}
