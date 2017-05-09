// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// NOTE:
// Because testutil is invoked by tests throughout the Upspin tree,
// it cannot import any Upspin packages.

// Package testutil includes utility functions for Upspin tests.
package testutil // import "upspin.io/test/testutil"

import (
	"go/build"
	"log"
	"path/filepath"
)

// Repo returns the local filename of a file in the Upspin repository.
func Repo(dir ...string) string {
	p, err := build.Import("upspin.io", "", build.FindOnly)
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(p.Dir, filepath.Join(dir...))
}
