// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin dragonfly freebsd linux nacl netbsd openbsd solaris

package flags

import (
	"os"
	"path/filepath"
)

var (
	// defaultCacheDir specifies the default directory for the various file caches.
	defaultCacheDir = filepath.Join(os.Getenv("HOME"), "upspin")

	// defaultConfig names the default Upspin configuration file to use.
	defaultConfig = filepath.Join(os.Getenv("HOME"), "upspin", "config")
)
