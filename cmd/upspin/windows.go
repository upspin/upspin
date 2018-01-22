// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package main

import (
	"os"
	"strings"
)

const envPath = "PATH"

// windowsPathExtensions returns the file extensions for executable files as a string
// slice.
// It evaluates and splits the PATHEXT environment variable, or, if the
// environment variable is empty, returns a sensible set of defaults.
func windowsPathExtensions() []string {
	var exts []string
	x := os.Getenv("PATHEXT")
	if x == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	} else {
		for _, e := range strings.Split(strings.ToLower(x), `;`) {
			if e == "" {
				continue
			}
			if e[0] != '.' {
				e = "." + e
			}
			exts = append(exts, e)
		}
	}
	return exts
}
