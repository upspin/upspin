// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// findUpspinBinaries finds all the upspin-* binaries in $PATH.
// It may return the same name multiple times; the caller should
// filter. (But all it's going to do is sort and print them, so it's easy.)
func findUpspinBinaries() []string {
	path := os.Getenv(envPath)
	var cmds []string
	windowsExts := windowsPathExtensions()
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
			name := info.Name()
			if !strings.HasPrefix(name, "upspin-") {
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if len(windowsExts) == 0 {
				// Not on Windows.
				if info.Mode().Perm()&0100 == 0 {
					continue
				}
				cmds = append(cmds, info.Name()[len("upspin-"):])
				continue
			}
			// On Windows, which is trickier because there is no execute bit and there
			// can be a .exe suffix (or even something else).
			for _, e := range windowsExts {
				if strings.HasSuffix(info.Name(), e) {
					cmds = append(cmds, name[len("upspin-"):len(name)-len(e)])
				}
			}
		}
	}
	return cmds
}
