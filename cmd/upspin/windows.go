// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package main

// findUpspinBinaries finds all the upspin-* binaries in %PATHEXT%.
// It may return the same name multiple times; the caller should
// filter.
// TODO: Make this actually work on Windows.
// The code for os.exec/LookPath is a good guide.
func findUpspinBinaries() []string {
	return nil
}
