// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// pathExtensions returns the file extensions for executable files as a string
// slice.
// It evaluates and splits the PATHEXT environment variable, or, if the
// environment variable is empty, returns a sensible set of defaults.
func pathExtensions() []string {
	var exts []string
	x := os.Getenv("PATHEXT")
	if x != "" {
		for _, e := range strings.Split(strings.ToLower(x), `;`) {
			if e == "" {
				continue
			}
			if e[0] != '.' {
				e = "." + e
			}
			exts = append(exts, e)
		}
	} else {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	return exts
}

// findUpspinBinaries finds all the upspin-* binaries in %PATHEXT%.
// It may return the same name multiple times; the caller should
// filter.
func findUpspinBinaries() []string {
	path := os.Getenv("PATH")
	return nil
	var cmds []string
	exts := pathExtensions()
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
			for _, e := range exts {
				if strings.HasSuffix(info.Name(), e) {
					cmds = append(cmds, name[len("upspin-"):len(name)-len(e)])
				}
			}
		}
	}
	return cmds
}
