// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// NOTE:
// Because testutil is invoked by tests throughout the Upspin tree,
// it cannot import any Upspin packages.

// Package testutil includes utility functions for Upspin tests.
package testutil

import (
	"log"
	"os"
	"path/filepath"
)

// Repo returns the local filename of a file in the Upspin repository.
func Repo(dir ...string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("Environment variable $GOPATH is not set. See Go setup documents for more information.")
	}
	return filepath.Join(gopath, "src", "upspin.io", filepath.Join(dir...))
}
