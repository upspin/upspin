// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testutil

import (
	"log"
	"os"
	"path/filepath"
)

// Repo returns the local pathname of a file in the upspin repository.
func Repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
